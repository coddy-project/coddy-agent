//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// newNeuralDeepTestServer wires a Server around one neuraldeep provider.
func newNeuralDeepTestServer(t *testing.T, home string) *Server {
	t.Helper()
	cfg := &config.Config{
		Paths:     config.Paths{Home: home},
		Providers: []config.ProviderConfig{{Name: "neuraldeep", Type: "neuraldeep"}},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	return New(cfg, mgr, slog.Default(), t.TempDir())
}

func TestNeuralDeepAuthDeviceHTTPFlow(t *testing.T) {
	home := t.TempDir()
	// Another test in this package may have loaded a real $CODDY_HOME/.env
	// into the process environment; the credential-source assertions below
	// must not depend on the developer machine.
	t.Setenv("NEURALDEEP_API_KEY", "")
	var revoked atomic.Bool
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cli/device/start":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code": "dev-http", "user_code": "BCDF-2345",
				"verification_uri":          "http://hub/app/device",
				"verification_uri_complete": "http://hub/app/device?code=BCDF-2345",
				"interval":                  0, "expires_in": 900,
			})
		case "/api/cli/device/token":
			_, _ = fmt.Fprint(w, `{"access_token":"sk-http-flow","token_type":"bearer","label":"coddy @ host"}`)
		case "/api/cli/revoke":
			revoked.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer hub.Close()
	t.Setenv(llm.EnvNeuralDeepHubURL, hub.URL)

	srv := newNeuralDeepTestServer(t, home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	startRes, err := http.Post(ts.URL+"/coddy/providers/neuraldeep/neuraldeep-auth/device", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = startRes.Body.Close() }()
	if startRes.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d", startRes.StatusCode)
	}
	var start struct {
		LoginID         string `json:"login_id"`
		VerificationURL string `json:"verification_url"`
		UserCode        string `json:"user_code"`
	}
	if err := json.NewDecoder(startRes.Body).Decode(&start); err != nil {
		t.Fatal(err)
	}
	if start.LoginID == "" || start.UserCode != "BCDF-2345" || start.VerificationURL != "http://hub/app/device?code=BCDF-2345" {
		t.Fatalf("unexpected start response: %+v", start)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		statusRes, err := http.Get(ts.URL + "/coddy/providers/neuraldeep/neuraldeep-auth/device/" + start.LoginID)
		if err != nil {
			t.Fatal(err)
		}
		var status struct {
			Status    string `json:"status"`
			Connected bool   `json:"connected"`
			Error     string `json:"error"`
		}
		if err := json.NewDecoder(statusRes.Body).Decode(&status); err != nil {
			_ = statusRes.Body.Close()
			t.Fatal(err)
		}
		_ = statusRes.Body.Close()
		if status.Status == "completed" {
			if !status.Connected {
				t.Fatalf("completed but not connected: %+v", status)
			}
			break
		}
		if status.Status == "failed" {
			t.Fatalf("login failed: %s", status.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("login did not complete, last status: %+v", status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The stored key authenticates and the status shows a masked value only.
	if key, _ := llm.LoadNeuralDeepKey(config.NeuralDeepAuthPath(home, "neuraldeep")); key != "sk-http-flow" {
		t.Fatalf("stored key = %q", key)
	}
	stRes, err := http.Get(ts.URL + "/coddy/providers/neuraldeep/neuraldeep-auth")
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Connected bool   `json:"connected"`
		Masked    string `json:"masked"`
		Source    string `json:"source"`
	}
	if err := json.NewDecoder(stRes.Body).Decode(&st); err != nil {
		_ = stRes.Body.Close()
		t.Fatal(err)
	}
	_ = stRes.Body.Close()
	if !st.Connected || st.Source != "oauth" || st.Masked == "" || st.Masked == "sk-http-flow" {
		t.Fatalf("status = %+v, want connected oauth with a masked key", st)
	}

	// Sign out: server-side revoke attempted, local file gone, disconnected.
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/coddy/providers/neuraldeep/neuraldeep-auth", nil)
	delRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var after struct {
		Connected bool   `json:"connected"`
		Source    string `json:"source"`
	}
	if err := json.NewDecoder(delRes.Body).Decode(&after); err != nil {
		_ = delRes.Body.Close()
		t.Fatal(err)
	}
	_ = delRes.Body.Close()
	if after.Connected || after.Source != "none" {
		t.Fatalf("after sign-out: %+v", after)
	}
	if !revoked.Load() {
		t.Fatal("sign-out must attempt the hub-side revoke")
	}
	if _, err := os.Stat(config.NeuralDeepAuthPath(home, "neuraldeep")); !os.IsNotExist(err) {
		t.Fatal("auth file must be removed on sign-out")
	}
	srv.Drain()
}

// TestNeuralDeepAuthDeviceLoginDrains pins that a sign-in nobody approves
// does not outlive the server: Drain cancels the polling goroutine.
func TestNeuralDeepAuthDeviceLoginDrains(t *testing.T) {
	home := t.TempDir()
	var polls atomic.Int64
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cli/device/start":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code": "dev-drain", "user_code": "DRAI-NNNN",
				"verification_uri": "http://hub/app/device", "interval": 0, "expires_in": 900,
			})
		case "/api/cli/device/token":
			polls.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":"authorization_pending"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer hub.Close()
	t.Setenv(llm.EnvNeuralDeepHubURL, hub.URL)

	srv := newNeuralDeepTestServer(t, home)
	ts := httptest.NewServer(srv.Handler())

	res, err := http.Post(ts.URL+"/coddy/providers/neuraldeep/neuraldeep-auth/device", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d", res.StatusCode)
	}
	deadline := time.Now().Add(2 * time.Second)
	for polls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("device login never polled the hub")
		}
		time.Sleep(5 * time.Millisecond)
	}

	ts.Close()
	done := make(chan struct{})
	go func() {
		srv.Drain()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Drain did not return: a pending NeuralDeep sign-in blocks shutdown")
	}
	settled := polls.Load()
	time.Sleep(200 * time.Millisecond)
	if got := polls.Load(); got != settled {
		t.Fatalf("pending sign-in kept polling after Drain (%d -> %d)", settled, got)
	}
}

func TestNeuralDeepAuthEdges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NEURALDEEP_API_KEY", "")
	srv := newNeuralDeepTestServer(t, home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Drain()

	// Unknown login id is 404, never a key.
	res, err := http.Get(ts.URL + "/coddy/providers/neuraldeep/neuraldeep-auth/device/nope")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown login status = %d, want 404", res.StatusCode)
	}

	// A provider of another type conflicts.
	res, err = http.Get(ts.URL + "/coddy/providers/neuraldeep2/neuraldeep-auth")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unsaved valid name must be accepted (got %d)", res.StatusCode)
	}

	// An explicit api_key shadows the stored login: source says so.
	cfg := srv.activeCfg()
	cfg.Providers[0].APIKey = "sk-manual"
	if err := llm.SaveNeuralDeepAuth(config.NeuralDeepAuthPath(home, "neuraldeep"), "sk-oauth", "http://hub", "coddy", "coddy"); err != nil {
		t.Fatal(err)
	}
	stRes, err := http.Get(ts.URL + "/coddy/providers/neuraldeep/neuraldeep-auth")
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Connected bool   `json:"connected"`
		Source    string `json:"source"`
	}
	if err := json.NewDecoder(stRes.Body).Decode(&st); err != nil {
		_ = stRes.Body.Close()
		t.Fatal(err)
	}
	_ = stRes.Body.Close()
	if !st.Connected || st.Source != "api_key" {
		t.Fatalf("shadowed status = %+v, want connected with source api_key", st)
	}
}

// TestNeuralDeepAuthDeviceHonorsSelectedEndpoint covers the edges of the
// endpoint carried by the settings form: the device start takes the pick
// from its body (unknown values are refused before any hub is contacted),
// and the status endpoint reports which hub a sign-in for a given endpoint
// would use next to the hub the stored login came from.
func TestNeuralDeepAuthDeviceHonorsSelectedEndpoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NEURALDEEP_API_KEY", "")
	t.Setenv(llm.EnvNeuralDeepHubURL, "")
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cli/device/start":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code": "dev-sel", "user_code": "SELX-0001",
				"verification_uri": "http://hub/app/device", "interval": 0, "expires_in": 900,
			})
		case "/api/cli/device/token":
			// Never completes: the wait is drained with the server.
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":"authorization_pending"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer hub.Close()

	srv := newNeuralDeepTestServer(t, home)
	var mu sync.Mutex
	var asked []string
	srv.neuralDeepHubFor = func(apiBase string) string {
		mu.Lock()
		asked = append(asked, apiBase)
		mu.Unlock()
		return hub.URL
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Drain()

	post := func(body string) (*http.Response, string) {
		t.Helper()
		var payload io.Reader
		if body != "" {
			payload = strings.NewReader(body)
		}
		res, err := http.Post(ts.URL+"/coddy/providers/neuraldeep/neuraldeep-auth/device", "application/json", payload)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		return res, string(raw)
	}

	// An endpoint outside the allowlist is refused before any hub is asked:
	// minting a key for the wrong deployment would only surface later.
	res, body := post(`{"api_base":"https://example.invalid/v1"}`)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "not a NeuralDeep endpoint") {
		t.Fatalf("unknown api_base: status %d body %s, want 400 naming the endpoint", res.StatusCode, body)
	}
	res, body = post(`{"api_base": 42`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed body: status %d body %s, want 400", res.StatusCode, body)
	}
	mu.Lock()
	if len(asked) != 0 {
		t.Fatalf("a refused start must not resolve a hub, asked %v", asked)
	}
	mu.Unlock()

	// The pick is normalized on the way to the hub resolver; an empty body
	// falls back to the saved row (which has no api_base here).
	if res, body = post(`{"api_base":"https://api.neuraldeep.tech/v1/"}`); res.StatusCode != http.StatusOK {
		t.Fatalf("mirror start: status %d body %s", res.StatusCode, body)
	}
	if res, body = post(""); res.StatusCode != http.StatusOK {
		t.Fatalf("plain start: status %d body %s", res.StatusCode, body)
	}
	mu.Lock()
	got := append([]string(nil), asked...)
	mu.Unlock()
	if len(got) != 2 || got[0] != "https://api.neuraldeep.tech/v1" || got[1] != "" {
		t.Fatalf("hub resolved for %v, want [mirror, \"\"]", got)
	}

	// Status: hub is where the stored login came from, endpoint_hub is what a
	// sign-in for the queried endpoint would use (the saved row when absent).
	if err := llm.SaveNeuralDeepAuth(config.NeuralDeepAuthPath(home, "neuraldeep"), "sk-from-ru", "https://hub.neuraldeep.ru", "coddy", "coddy"); err != nil {
		t.Fatal(err)
	}
	srv.neuralDeepHubFor = func(apiBase string) string {
		if base, ok := llm.NormalizeNeuralDeepAPIBase(apiBase); ok && base == "https://api.neuraldeep.tech/v1" {
			return "https://hub.neuraldeep.tech"
		}
		return "https://hub.neuraldeep.ru"
	}
	status := func(query string) map[string]any {
		t.Helper()
		res, err := http.Get(ts.URL + "/coddy/providers/neuraldeep/neuraldeep-auth" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status%s = %d", query, res.StatusCode)
		}
		var doc map[string]any
		if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	if doc := status("?api_base=https%3A%2F%2Fapi.neuraldeep.tech%2Fv1"); doc["hub"] != "https://hub.neuraldeep.ru" || doc["endpoint_hub"] != "https://hub.neuraldeep.tech" {
		t.Fatalf("status for the mirror = %+v", doc)
	}
	if doc := status(""); doc["hub"] != "https://hub.neuraldeep.ru" || doc["endpoint_hub"] != "https://hub.neuraldeep.ru" {
		t.Fatalf("status for the saved row = %+v", doc)
	}
	// An unrecognized query value counts as omitted: the saved row decides.
	srv.activeCfg().Providers[0].APIBase = "https://api.neuraldeep.tech/v1"
	if doc := status("?api_base=https%3A%2F%2Fexample.invalid%2Fv1"); doc["endpoint_hub"] != "https://hub.neuraldeep.tech" {
		t.Fatalf("status for an unknown endpoint must describe the saved row, got %+v", doc)
	}
}

// blockingStartHub is a stand-in hub whose first device/start call blocks
// until release is closed; later calls answer at once. Token polls return
// key immediately, so a surviving attempt completes.
func blockingStartHub(t *testing.T, key string) (hub *httptest.Server, started <-chan struct{}, release chan struct{}) {
	t.Helper()
	startedCh := make(chan struct{})
	release = make(chan struct{})
	var startCalls atomic.Int32
	hub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cli/device/start":
			if startCalls.Add(1) == 1 {
				close(startedCh)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code": "dev-block", "user_code": "BLCK-0001",
				"verification_uri": "http://hub/app/device", "interval": 0, "expires_in": 900,
			})
		case "/api/cli/device/token":
			_, _ = fmt.Fprint(w, `{"access_token":"`+key+`","token_type":"bearer","label":"coddy @ host"}`)
		case "/api/cli/revoke":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hub.Close)
	return hub, startedCh, release
}

func startDeviceLogin(t *testing.T, ts *httptest.Server) (int, string) {
	t.Helper()
	res, err := http.Post(ts.URL+"/coddy/providers/neuraldeep/neuraldeep-auth/device", "application/json", nil)
	if err != nil {
		t.Errorf("device start: %v", err)
		return 0, ""
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		LoginID string `json:"login_id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&body)
	return res.StatusCode, body.LoginID
}

func waitDeviceLogin(t *testing.T, ts *httptest.Server, loginID string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		res, err := http.Get(ts.URL + "/coddy/providers/neuraldeep/neuraldeep-auth/device/" + loginID)
		if err != nil {
			t.Fatal(err)
		}
		var st struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&st)
		_ = res.Body.Close()
		if st.Status == "completed" || st.Status == "failed" {
			return st.Status + " " + st.Error
		}
		if time.Now().After(deadline) {
			t.Fatalf("login %s did not settle, last %+v", loginID, st)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestNeuralDeepAuthDeviceStartSupersededWhileContactingHub: a second start
// for the same provider must supersede the first even while the first is
// still waiting for the hub to answer, otherwise the loser completes later
// and overwrites the newer credential.
func TestNeuralDeepAuthDeviceStartSupersededWhileContactingHub(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NEURALDEEP_API_KEY", "")
	hub, started, release := blockingStartHub(t, "sk-survivor")
	t.Setenv(llm.EnvNeuralDeepHubURL, hub.URL)

	srv := newNeuralDeepTestServer(t, home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Drain()

	firstDone := make(chan struct{})
	var firstStatus int
	go func() {
		defer close(firstDone)
		firstStatus, _ = startDeviceLogin(t, ts)
	}()
	<-started

	// The second start lands while the first is blocked inside the hub call.
	status, loginID := startDeviceLogin(t, ts)
	if status != http.StatusOK || loginID == "" {
		t.Fatalf("second start: status %d login %q", status, loginID)
	}
	// The hub is released only now, so the first start can only have
	// returned because it was cancelled, not because the hub answered.
	// (Released before ts.Close either way: a request still parked in the
	// hub would otherwise keep the test server from shutting down.)
	superseded := false
	select {
	case <-firstDone:
		superseded = true
	case <-time.After(3 * time.Second):
	}
	close(release)
	if !superseded {
		t.Fatal("the superseded start did not return while the hub was blocked")
	}
	if firstStatus != http.StatusConflict {
		t.Fatalf("superseded start status = %d, want 409", firstStatus)
	}
	if got := waitDeviceLogin(t, ts, loginID); !strings.HasPrefix(got, "completed") {
		t.Fatalf("surviving login = %q, want completed", got)
	}
	key, err := llm.LoadNeuralDeepKey(config.NeuralDeepAuthPath(home, "neuraldeep"))
	if err != nil || key != "sk-survivor" {
		t.Fatalf("stored key = %q (%v), want the survivor's", key, err)
	}
	srv.codexAuthMu.Lock()
	pending := 0
	for _, a := range srv.neuralDeepAuthLogins {
		if a.Status == "pending" {
			pending++
		}
	}
	srv.codexAuthMu.Unlock()
	if pending != 0 {
		t.Fatalf("%d attempt(s) still pending after the supersede", pending)
	}
}

// TestNeuralDeepAuthSignOutDuringHubStart: a sign-out that lands while a
// start is still contacting the hub must cancel that start too; otherwise it
// registers afterwards, completes, and re-stores a credential the user just
// removed.
func TestNeuralDeepAuthSignOutDuringHubStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NEURALDEEP_API_KEY", "")
	hub, started, release := blockingStartHub(t, "sk-resurrected")
	t.Setenv(llm.EnvNeuralDeepHubURL, hub.URL)

	srv := newNeuralDeepTestServer(t, home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Drain()

	firstDone := make(chan struct{})
	var firstStatus int
	go func() {
		defer close(firstDone)
		firstStatus, _ = startDeviceLogin(t, ts)
	}()
	<-started

	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/coddy/providers/neuraldeep/neuraldeep-auth", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("sign-out status = %d", res.StatusCode)
	}
	close(release)
	select {
	case <-firstDone:
	case <-time.After(3 * time.Second):
		t.Fatal("the cancelled start did not return")
	}
	if firstStatus != http.StatusConflict {
		t.Fatalf("cancelled start status = %d, want 409", firstStatus)
	}
	// Give a wrongly surviving wait every chance to store the key.
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(config.NeuralDeepAuthPath(home, "neuraldeep")); !os.IsNotExist(err) {
		t.Fatalf("credential must not reappear after sign-out (stat err %v)", err)
	}
	srv.codexAuthMu.Lock()
	n := len(srv.neuralDeepAuthLogins)
	srv.codexAuthMu.Unlock()
	if n != 0 {
		t.Fatalf("%d attempt(s) registered after a cancelled start", n)
	}
}

// TestNeuralDeepAuthDeviceStartHubFailureIs502: a hub that cannot start the
// flow is a gateway problem, not a supersede; the attempt must not linger.
func TestNeuralDeepAuthDeviceStartHubFailureIs502(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NEURALDEEP_API_KEY", "")
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "hub down", http.StatusInternalServerError)
	}))
	defer hub.Close()
	t.Setenv(llm.EnvNeuralDeepHubURL, hub.URL)

	srv := newNeuralDeepTestServer(t, home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Drain()

	status, loginID := startDeviceLogin(t, ts)
	if status != http.StatusBadGateway || loginID != "" {
		t.Fatalf("hub failure: status %d login %q, want 502 and no login id", status, loginID)
	}
	srv.codexAuthMu.Lock()
	n := len(srv.neuralDeepAuthLogins)
	srv.codexAuthMu.Unlock()
	if n != 0 {
		t.Fatalf("%d attempt(s) left registered after a failed start", n)
	}
}

// TestNeuralDeepPersistSkipsCancelledAttempt pins the last window: the key
// has been minted, the wait was cancelled meanwhile, and the credential must
// not be written.
func TestNeuralDeepPersistSkipsCancelledAttempt(t *testing.T) {
	home := t.TempDir()
	srv := newNeuralDeepTestServer(t, home)
	defer srv.Drain()
	authPath := config.NeuralDeepAuthPath(home, "neuraldeep")

	ctx, cancel := context.WithCancel(context.Background())
	attempt := &codexAuthLoginAttempt{ProviderName: "neuraldeep", Status: "pending", CreatedAt: time.Now(), cancel: cancel}
	cancel()
	if err := srv.persistNeuralDeepLogin(ctx, attempt, authPath, "sk-late", "https://hub.neuraldeep.ru", "coddy"); err == nil {
		t.Fatal("a cancelled attempt must not persist its key")
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("credential written for a cancelled attempt (stat err %v)", err)
	}
	if attempt.Status != "pending" || attempt.Connected {
		t.Fatalf("attempt = %+v, want untouched", attempt)
	}

	live := &codexAuthLoginAttempt{ProviderName: "neuraldeep", Status: "pending", CreatedAt: time.Now()}
	if err := srv.persistNeuralDeepLogin(context.Background(), live, authPath, "sk-live", "https://hub.neuraldeep.tech", "coddy"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	st, err := llm.InspectNeuralDeepAuth(authPath)
	if err != nil || !st.Connected || st.Hub != "https://hub.neuraldeep.tech" {
		t.Fatalf("stored status = %+v (%v), want connected via the mirror hub", st, err)
	}
	if live.Status != "completed" || !live.Connected {
		t.Fatalf("attempt = %+v, want completed", live)
	}
}
