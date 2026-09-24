//go:build http

package httpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/proxytest"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func codexHTTPTestJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(claims)
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// TestCodexAuthDeviceLoginDrains pins that a sign-in still waiting for browser
// confirmation does not outlive the server: Drain must cancel it instead of
// leaving a goroutine that writes credentials into a torn-down home directory.
func TestCodexAuthDeviceLoginDrains(t *testing.T) {
	home := t.TempDir()
	var polls atomic.Int64
	authUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_, _ = fmt.Fprint(w, `{"device_auth_id":"device-drain","user_code":"DRAIN","interval":"0.01"}`)
		case "/api/accounts/deviceauth/token":
			// The user never confirms in the browser.
			polls.Add(1)
			w.WriteHeader(http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer authUpstream.Close()

	cfg := &config.Config{
		Paths:     config.Paths{Home: home},
		Providers: []config.ProviderConfig{{Name: "codex", Type: "codex"}},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	srv.codexAuthIssuer = authUpstream.URL
	ts := httptest.NewServer(srv.Handler())

	res, err := http.Post(ts.URL+"/coddy/providers/codex/codex-auth/device", "application/json", nil)
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
			t.Fatal("device login never polled the issuer")
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
		t.Fatal("Drain did not return: a pending Codex sign-in blocks shutdown")
	}

	// After Drain the sign-in must be gone, not merely unobserved.
	settled := polls.Load()
	time.Sleep(200 * time.Millisecond)
	if got := polls.Load(); got != settled {
		t.Fatalf("pending Codex sign-in kept polling after Drain (%d -> %d)", settled, got)
	}
}

func TestCodexAuthDeviceHTTPFlow(t *testing.T) {
	home := t.TempDir()
	idToken := codexHTTPTestJWT(map[string]any{"chatgpt_account_id": "acct-http"})
	accessToken := codexHTTPTestJWT(map[string]any{"exp": 4_102_444_800})
	authUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_, _ = fmt.Fprint(w, `{"device_auth_id":"device-http","user_code":"HTTP-CODE","interval":"0"}`)
		case "/api/accounts/deviceauth/token":
			_, _ = fmt.Fprint(w, `{"authorization_code":"code-http","code_challenge":"challenge-http","code_verifier":"verifier-http"}`)
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id_token": idToken, "access_token": accessToken, "refresh_token": "refresh-http",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authUpstream.Close()

	cfg := &config.Config{
		Paths:     config.Paths{Home: home},
		Providers: []config.ProviderConfig{{Name: "codex", Type: "codex"}},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	srv.codexAuthIssuer = authUpstream.URL
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	startRes, err := http.Post(ts.URL+"/coddy/providers/codex/codex-auth/device", "application/json", nil)
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
	if start.LoginID == "" || start.UserCode != "HTTP-CODE" || start.VerificationURL != authUpstream.URL+"/codex/device" {
		t.Fatalf("unexpected start response: %+v", start)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		statusRes, err := http.Get(ts.URL + "/coddy/providers/codex/codex-auth/device/" + start.LoginID)
		if err != nil {
			t.Fatal(err)
		}
		var status struct {
			Status    string `json:"status"`
			Connected bool   `json:"connected"`
		}
		if err := json.NewDecoder(statusRes.Body).Decode(&status); err != nil {
			_ = statusRes.Body.Close()
			t.Fatal(err)
		}
		_ = statusRes.Body.Close()
		if status.Status == "completed" {
			if !status.Connected {
				t.Fatalf("completed status is not connected: %+v", status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("login did not complete, last status: %+v", status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	authPath := config.CodexAuthPath(home, "codex")
	if filepath.Dir(authPath) == home {
		t.Fatalf("auth path must be namespaced: %s", authPath)
	}
	statusRes, err := http.Get(ts.URL + "/coddy/providers/codex/codex-auth")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = statusRes.Body.Close() }()
	var status struct {
		Connected bool   `json:"connected"`
		Source    string `json:"source"`
	}
	if err := json.NewDecoder(statusRes.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if !status.Connected || status.Source != "coddy" {
		t.Fatalf("saved status = %+v", status)
	}
}

// TestCodexAuthDeviceStartGoesThroughTheRowsProxy pins that the Settings
// sign-in of a codex row asks the OAuth issuer through the proxy the row
// names, like every other request of that row.
func TestCodexAuthDeviceStartGoesThroughTheRowsProxy(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/accounts/deviceauth/usercode" {
			_, _ = fmt.Fprint(w, `{"device_auth_id":"device-proxy","user_code":"PRXY","interval":"5"}`)
			return
		}
		// Nobody confirms in the browser.
		w.WriteHeader(http.StatusForbidden)
	}))
	defer issuer.Close()
	proxy := proxytest.New()
	defer proxy.Close()

	cfg := &config.Config{
		Paths:     config.Paths{Home: t.TempDir()},
		Providers: []config.ProviderConfig{{Name: "codex", Type: "codex", Proxy: proxy.URL()}},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	srv.codexAuthIssuer = issuer.URL
	ts := httptest.NewServer(srv.Handler())
	defer func() {
		ts.Close()
		srv.Drain()
	}()

	res, err := http.Post(ts.URL+"/coddy/providers/codex/codex-auth/device", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d", res.StatusCode)
	}
	if carried := proxy.Carried(); !slices.Contains(carried, "/api/accounts/deviceauth/usercode") {
		t.Fatalf("the device start did not go through the row's proxy; it carried %v", carried)
	}
}

// TestCodexAuthRowSwitchedFromAnotherType: the settings form can switch a
// saved row to codex and sign it in before the save (issue #334 names the
// same refusal for neuraldeep). The sign-in routes accept the row, and its
// own proxy still carries the device start.
func TestCodexAuthRowSwitchedFromAnotherType(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/accounts/deviceauth/usercode" {
			_, _ = fmt.Fprint(w, `{"device_auth_id":"device-switch","user_code":"SWCH","interval":"5"}`)
			return
		}
		// Nobody confirms in the browser.
		w.WriteHeader(http.StatusForbidden)
	}))
	defer issuer.Close()
	proxy := proxytest.New()
	defer proxy.Close()

	cfg := &config.Config{
		Paths: config.Paths{Home: t.TempDir()},
		Providers: []config.ProviderConfig{{
			Name: "openai", Type: "openai", APIKey: "sk-openai-row", Proxy: proxy.URL(),
		}},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	srv.codexAuthIssuer = issuer.URL
	ts := httptest.NewServer(srv.Handler())
	defer func() {
		ts.Close()
		srv.Drain()
	}()
	endpoint := ts.URL + "/coddy/providers/openai/codex-auth"

	res, err := http.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status read = %d, want 200 for a row the form is switching to codex", res.StatusCode)
	}
	res, err = http.Post(endpoint+"/device", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("device start = %d, want 200", res.StatusCode)
	}
	if carried := proxy.Carried(); !slices.Contains(carried, "/api/accounts/deviceauth/usercode") {
		t.Fatalf("the device start did not go through the row's proxy; it carried %v", carried)
	}
	req, _ := http.NewRequest(http.MethodDelete, endpoint, nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("sign-out = %d, want 200", res.StatusCode)
	}
}

// codexIssuerStandIn is a stand-in ChatGPT OAuth issuer for the sign-in
// races. Every device start gets its own device_auth_id ("device-1",
// "device-2", ...), and the account id of the credential it finally mints
// names that id, so a test can tell whose login reached the disk. The first
// start blocks until releaseFirst is closed when blockFirst is set; token
// polls stay pending until confirm is closed (the user has not approved in
// the browser yet).
type codexIssuerStandIn struct {
	srv          *httptest.Server
	started      chan struct{}
	releaseFirst chan struct{}
	confirm      chan struct{}
	starts       atomic.Int32
	polls        atomic.Int32
}

func newCodexIssuerStandIn(t *testing.T, blockFirst bool) *codexIssuerStandIn {
	t.Helper()
	is := &codexIssuerStandIn{
		started:      make(chan struct{}),
		releaseFirst: make(chan struct{}),
		confirm:      make(chan struct{}),
	}
	var startedOnce sync.Once
	accessToken := codexHTTPTestJWT(map[string]any{"exp": 4_102_444_800})
	is.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			n := is.starts.Add(1)
			startedOnce.Do(func() { close(is.started) })
			if blockFirst && n == 1 {
				select {
				case <-is.releaseFirst:
				case <-r.Context().Done():
					return
				}
			}
			_, _ = fmt.Fprintf(w, `{"device_auth_id":"device-%d","user_code":"CODE-%d","interval":"0"}`, n, n)
		case "/api/accounts/deviceauth/token":
			is.polls.Add(1)
			select {
			case <-is.confirm:
			default:
				w.WriteHeader(http.StatusForbidden)
				return
			}
			var body struct {
				DeviceAuthID string `json:"device_auth_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_, _ = fmt.Fprintf(w, `{"authorization_code":"code-%s","code_challenge":"c","code_verifier":"v"}`, body.DeviceAuthID)
		case "/oauth/token":
			_ = r.ParseForm()
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id_token":      codexHTTPTestJWT(map[string]any{"chatgpt_account_id": "acct-" + r.PostForm.Get("code")}),
				"access_token":  accessToken,
				"refresh_token": "refresh-" + r.PostForm.Get("code"),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(is.srv.Close)
	return is
}

func newCodexTestServer(t *testing.T, home, issuer string) *Server {
	t.Helper()
	cfg := &config.Config{
		Paths:     config.Paths{Home: home},
		Providers: []config.ProviderConfig{{Name: "codex", Type: "codex"}},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	srv.codexAuthIssuer = issuer
	return srv
}

func startCodexDeviceLogin(t *testing.T, ts *httptest.Server) (int, string) {
	t.Helper()
	res, err := http.Post(ts.URL+"/coddy/providers/codex/codex-auth/device", "application/json", strings.NewReader("{}"))
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

func waitCodexDeviceLogin(t *testing.T, ts *httptest.Server, loginID string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		res, err := http.Get(ts.URL + "/coddy/providers/codex/codex-auth/device/" + loginID)
		if err != nil {
			t.Fatal(err)
		}
		var st codexAuthLoginResponse
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

// storedCodexAccount reads the account id of the credential on disk, "" when
// there is none.
func storedCodexAccount(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(config.CodexAuthPath(home, "codex"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Tokens struct {
			AccountID string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	return file.Tokens.AccountID
}

// TestCodexAuthSignOutCancelsPendingLogin: a sign-out while a device login
// still waits for the browser must end that wait. Otherwise the user
// finishes approving in the tab that is still open, the wait completes and
// re-stores the credential the user has just removed.
func TestCodexAuthSignOutCancelsPendingLogin(t *testing.T) {
	home := t.TempDir()
	issuer := newCodexIssuerStandIn(t, false)
	srv := newCodexTestServer(t, home, issuer.srv.URL)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Drain()

	status, loginID := startCodexDeviceLogin(t, ts)
	if status != http.StatusOK || loginID == "" {
		t.Fatalf("device start: status %d login %q", status, loginID)
	}
	deadline := time.Now().Add(2 * time.Second)
	for issuer.polls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the device login never polled the issuer")
		}
		time.Sleep(5 * time.Millisecond)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/coddy/providers/codex/codex-auth", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("sign-out status = %d", res.StatusCode)
	}
	// The user approves in the browser tab that is still open.
	close(issuer.confirm)

	if got := waitCodexDeviceLogin(t, ts, loginID); !strings.HasPrefix(got, "failed") {
		t.Fatalf("login after sign-out = %q, want failed", got)
	}
	// Give a wrongly surviving wait every chance to store the credential.
	time.Sleep(100 * time.Millisecond)
	if account := storedCodexAccount(t, home); account != "" {
		t.Fatalf("credential reappeared after sign-out (account %q)", account)
	}
}

// TestCodexAuthDeviceStartSupersededWhileContactingIssuer: a second start for
// the same provider must supersede the first even while the first still
// waits for the issuer to answer; otherwise both complete and the one that
// finishes last owns the credential, whichever login the user approved.
func TestCodexAuthDeviceStartSupersededWhileContactingIssuer(t *testing.T) {
	home := t.TempDir()
	issuer := newCodexIssuerStandIn(t, true)
	close(issuer.confirm)
	srv := newCodexTestServer(t, home, issuer.srv.URL)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Drain()

	firstDone := make(chan struct{})
	var firstStatus int
	go func() {
		defer close(firstDone)
		firstStatus, _ = startCodexDeviceLogin(t, ts)
	}()
	<-issuer.started

	// The second start lands while the first is blocked inside the issuer.
	status, loginID := startCodexDeviceLogin(t, ts)
	if status != http.StatusOK || loginID == "" {
		t.Fatalf("second start: status %d login %q", status, loginID)
	}
	superseded := false
	select {
	case <-firstDone:
		superseded = true
	case <-time.After(3 * time.Second):
	}
	// Released either way: a request parked in the issuer would otherwise
	// keep the test server from shutting down.
	close(issuer.releaseFirst)
	if !superseded {
		<-firstDone
		t.Fatal("the superseded start did not return while the issuer was blocked")
	}
	if firstStatus != http.StatusConflict {
		t.Fatalf("superseded start status = %d, want 409", firstStatus)
	}
	if got := waitCodexDeviceLogin(t, ts, loginID); !strings.HasPrefix(got, "completed") {
		t.Fatalf("surviving login = %q, want completed", got)
	}
	time.Sleep(100 * time.Millisecond)
	if account := storedCodexAccount(t, home); account != "acct-code-device-2" {
		t.Fatalf("stored account = %q, want the surviving login's acct-code-device-2", account)
	}
	srv.codexAuthMu.Lock()
	pending := 0
	for _, a := range srv.codexAuthLogins {
		if a.Status == "pending" {
			pending++
		}
	}
	srv.codexAuthMu.Unlock()
	if pending != 0 {
		t.Fatalf("%d attempt(s) still pending after the supersede", pending)
	}
}

// TestSignInDeviceStartsRequireJSON: a page on another site can make a
// browser POST to a loopback server without a preflight only as a "simple"
// request (text/plain, a form encoding). Both device starts must refuse
// those before any issuer or hub is contacted, or such a page could cancel
// the sign-in the user is in the middle of.
func TestSignInDeviceStartsRequireJSON(t *testing.T) {
	home := t.TempDir()
	issuer := newCodexIssuerStandIn(t, false)
	var hubHits atomic.Int32
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hubHits.Add(1)
		http.NotFound(w, r)
	}))
	defer hub.Close()
	t.Setenv(llm.EnvNeuralDeepHubURL, hub.URL)

	srv := newCodexTestServer(t, home, issuer.srv.URL)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Drain()

	for _, route := range []string{"codex/codex-auth/device", "neuraldeep/neuraldeep-auth/device"} {
		for _, contentType := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", ""} {
			req, _ := http.NewRequest(http.MethodPost, ts.URL+"/coddy/providers/"+route, strings.NewReader(`{}`))
			if contentType != "" {
				req.Header.Set("Content-Type", contentType)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != http.StatusUnsupportedMediaType {
				t.Errorf("POST %s with Content-Type %q = %d, want 415", route, contentType, res.StatusCode)
			}
		}
	}
	if n := issuer.starts.Load(); n != 0 {
		t.Errorf("the Codex issuer was contacted %d time(s)", n)
	}
	if n := hubHits.Load(); n != 0 {
		t.Errorf("the NeuralDeep hub was contacted %d time(s)", n)
	}
}

// TestCodexPersistSkipsCancelledAttempt pins the last window: the issuer has
// handed over the credential, the wait was cancelled meanwhile, and nothing
// may be written.
func TestCodexPersistSkipsCancelledAttempt(t *testing.T) {
	home := t.TempDir()
	srv := newCodexTestServer(t, home, "http://issuer.invalid")
	defer srv.Drain()
	authPath := config.CodexAuthPath(home, "codex")
	credential := []byte(`{"tokens":{"account_id":"acct-late"}}`)

	ctx, cancel := context.WithCancel(context.Background())
	attempt := &codexAuthLoginAttempt{ProviderName: "codex", Status: "pending", CreatedAt: time.Now(), cancel: cancel}
	cancel()
	if err := srv.persistCodexLogin(ctx, attempt, authPath, credential); err == nil {
		t.Fatal("a cancelled attempt must not persist its credential")
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("credential written for a cancelled attempt (stat err %v)", err)
	}
	if attempt.Status != "pending" || attempt.Connected {
		t.Fatalf("attempt = %+v, want untouched", attempt)
	}

	live := &codexAuthLoginAttempt{ProviderName: "codex", Status: "pending", CreatedAt: time.Now()}
	if err := srv.persistCodexLogin(context.Background(), live, authPath, credential); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if account := storedCodexAccount(t, home); account != "acct-late" {
		t.Fatalf("stored account = %q, want acct-late", account)
	}
	if live.Status != "completed" || !live.Connected {
		t.Fatalf("attempt = %+v, want completed", live)
	}
}
