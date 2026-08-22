//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
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
