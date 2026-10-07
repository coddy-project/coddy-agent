//go:build http

package httpserver

// The sign-in routes must take the proxy the caller names, not only the one
// the saved provider row carries: the Settings form signs a row in before it
// is saved, so the proxy typed there has to reach the issuer, the hub and the
// revoke call. The device-start bodies carry an optional "proxy" field
// (absent = the resolved row's setting, "" or "inherit" = the environment's
// proxy, "none" or a URL = exactly that setting), and the NeuralDeep sign-out
// accepts the same semantics as a ?proxy= query because a DELETE has no body.

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
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/proxytest"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// signInTestServer builds a Server with a single provider row the caller
// chooses, so the saved type and saved proxy vary per case.
func signInTestServer(t *testing.T, home string, provider config.ProviderConfig, issuer string) *Server {
	t.Helper()
	cfg := &config.Config{
		Paths:     config.Paths{Home: home},
		Providers: []config.ProviderConfig{provider},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	if issuer != "" {
		srv.codexAuthIssuer = issuer
	}
	return srv
}

func codexSignInIssuer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_, _ = io.WriteString(w, `{"device_auth_id":"da-1","user_code":"ABCD-EFGH","interval":"0"}`)
		case "/api/accounts/deviceauth/token":
			_, _ = io.WriteString(w, `{"authorization_code":"ac-1","code_challenge":"cc","code_verifier":"cv"}`)
		case "/oauth/token":
			_, _ = io.WriteString(w, `{"id_token":"x.y.z","access_token":"at","refresh_token":"rt"}`)
		default:
			http.NotFound(w, r)
		}
	}))
}

func postDeviceStart(t *testing.T, base, providerType, body string) *http.Response {
	t.Helper()
	res, err := http.Post(base+"/coddy/providers/x/"+providerType+"-auth/device",
		"application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// The unsaved proxy the form posts is honoured for a codex sign-in.
func TestCodexDeviceStartFollowsBodyProxy(t *testing.T) {
	issuer := codexSignInIssuer(t)
	defer issuer.Close()
	prox := proxytest.New()
	defer prox.Close()

	srv := signInTestServer(t, t.TempDir(),
		config.ProviderConfig{Name: "x", Type: "codex", Proxy: "none"}, issuer.URL)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res := postDeviceStart(t, ts.URL, "codex", `{"proxy":"`+prox.URL()+`"}`)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("device start = %d: %s", res.StatusCode, body)
	}
	if carried := prox.Carried(); len(carried) == 0 || carried[0] != "/api/accounts/deviceauth/usercode" {
		t.Fatalf("the issuer request did not go through the posted proxy; carried: %v", carried)
	}
}

// A saved row of another type keeps the sign-in on the posted proxy, not on
// whatever proxy the old row had.
func TestCodexDeviceStartBodyProxyOnTypeSwitch(t *testing.T) {
	issuer := codexSignInIssuer(t)
	defer issuer.Close()
	prox := proxytest.New()
	defer prox.Close()

	srv := signInTestServer(t, t.TempDir(),
		config.ProviderConfig{Name: "x", Type: "openai", Proxy: "none"}, issuer.URL)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res := postDeviceStart(t, ts.URL, "codex", `{"proxy":"`+prox.URL()+`"}`)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("device start = %d: %s", res.StatusCode, body)
	}
	if carried := prox.Carried(); len(carried) == 0 {
		t.Fatalf("the issuer request did not go through the posted proxy; carried: %v", carried)
	}
}

// "none" in the body means connect directly, even when the saved row names a proxy.
func TestCodexDeviceStartBodyProxyNoneOverridesSaved(t *testing.T) {
	issuer := codexSignInIssuer(t)
	defer issuer.Close()
	savedProx := proxytest.New()
	defer savedProx.Close()

	home, err := os.MkdirTemp("", "coddy-codex-signin-*")
	if err != nil {
		t.Fatal(err)
	}
	srv := signInTestServer(t, home,
		config.ProviderConfig{Name: "x", Type: "codex", Proxy: savedProx.URL()}, issuer.URL)
	defer func() {
		srv.Drain()
		_ = os.RemoveAll(home)
	}()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res := postDeviceStart(t, ts.URL, "codex", `{"proxy":"none"}`)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("device start = %d: %s", res.StatusCode, body)
	}
	if carried := savedProx.Carried(); len(carried) != 0 {
		t.Fatalf("\"none\" in the body must connect directly, but the saved proxy carried %v", carried)
	}
}

// An empty or "inherit" body proxy follows the environment, not the saved row.
func TestCodexDeviceStartBodyProxyInheritOverridesSaved(t *testing.T) {
	issuer := codexSignInIssuer(t)
	defer issuer.Close()
	savedProx := proxytest.New()
	defer savedProx.Close()

	srv := signInTestServer(t, t.TempDir(),
		config.ProviderConfig{Name: "x", Type: "codex", Proxy: savedProx.URL()}, issuer.URL)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, body := range []string{`{"proxy":""}`, `{"proxy":"inherit"}`} {
		res := postDeviceStart(t, ts.URL, "codex", body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("body %s: device start = %d", body, res.StatusCode)
		}
	}
	if carried := savedProx.Carried(); len(carried) != 0 {
		t.Fatalf("an empty body proxy must follow the environment, not the saved row; carried: %v", carried)
	}
}

// A proxy the client cannot spell is rejected before any request is made.
func TestCodexDeviceStartRejectsInvalidProxy(t *testing.T) {
	issuer := codexSignInIssuer(t)
	defer issuer.Close()
	srv := signInTestServer(t, t.TempDir(),
		config.ProviderConfig{Name: "x", Type: "codex"}, issuer.URL)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res := postDeviceStart(t, ts.URL, "codex", `{"proxy":"http://[::1"}`)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid proxy must be a 400, got %d", res.StatusCode)
	}
}

// Without a posted proxy the saved row's setting stands (the previous contract).
func TestCodexDeviceStartWithoutBodyProxyUsesRow(t *testing.T) {
	issuer := codexSignInIssuer(t)
	defer issuer.Close()
	prox := proxytest.New()
	defer prox.Close()

	srv := signInTestServer(t, t.TempDir(),
		config.ProviderConfig{Name: "x", Type: "codex", Proxy: prox.URL()}, issuer.URL)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res := postDeviceStart(t, ts.URL, "codex", `{}`)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("device start = %d: %s", res.StatusCode, body)
	}
	if carried := prox.Carried(); len(carried) == 0 {
		t.Fatalf("the saved row's proxy should have carried the issuer request; carried: %v", carried)
	}
}

// NeuralDeep device start honours the posted proxy the same way.
func TestNeuralDeepDeviceStartFollowsBodyProxy(t *testing.T) {
	hub := neuralDeepSignInHub(t)
	defer hub.Close()
	prox := proxytest.New()
	defer prox.Close()
	t.Setenv(llm.EnvNeuralDeepHubURL, hub.URL)

	home, err := os.MkdirTemp("", "coddy-neuraldeep-signin-*")
	if err != nil {
		t.Fatal(err)
	}
	srv := signInTestServer(t, home,
		config.ProviderConfig{Name: "x", Type: "neuraldeep", Proxy: "none"}, "")
	defer func() {
		srv.Drain()
		_ = os.RemoveAll(home)
	}()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res := postDeviceStart(t, ts.URL, "neuraldeep", `{"proxy":"`+prox.URL()+`"}`)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("device start = %d: %s", res.StatusCode, body)
	}
	if carried := prox.Carried(); len(carried) == 0 || carried[0] != "/api/cli/device/start" {
		t.Fatalf("the hub request did not go through the posted proxy; carried: %v", carried)
	}
}

func TestNeuralDeepDeviceStartRejectsInvalidProxy(t *testing.T) {
	hub := neuralDeepSignInHub(t)
	defer hub.Close()
	t.Setenv(llm.EnvNeuralDeepHubURL, hub.URL)

	srv := signInTestServer(t, t.TempDir(),
		config.ProviderConfig{Name: "x", Type: "neuraldeep"}, "")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res := postDeviceStart(t, ts.URL, "neuraldeep", `{"proxy":"http://[::1"}`)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid proxy must be a 400, got %d", res.StatusCode)
	}
}

// The sign-out's ?proxy= query picks the revoke route just like the body's
// field picks the start route.
func TestNeuralDeepAuthDeleteFollowsQueryProxy(t *testing.T) {
	hub := neuralDeepSignInHub(t)
	defer hub.Close()
	prox := proxytest.New()
	defer prox.Close()
	t.Setenv(llm.EnvNeuralDeepHubURL, hub.URL)

	home := t.TempDir()
	authPath := config.NeuralDeepAuthPath(home, "x")
	if err := llm.SaveNeuralDeepAuth(authPath, "key-1", hub.URL, "coddy", "bdd"); err != nil {
		t.Fatal(err)
	}
	srv := signInTestServer(t, home,
		config.ProviderConfig{Name: "x", Type: "neuraldeep", Proxy: "none"}, "")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodDelete,
		ts.URL+"/coddy/providers/x/neuraldeep-auth?proxy="+prox.URL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("sign-out = %d: %s", res.StatusCode, body)
	}
	for _, path := range prox.Carried() {
		if path == "/api/cli/revoke" {
			return
		}
	}
	t.Fatalf("the revoke did not go through ?proxy=; carried: %v", prox.Carried())
}

func TestNeuralDeepAuthDeleteRejectsInvalidProxy(t *testing.T) {
	hub := neuralDeepSignInHub(t)
	defer hub.Close()
	t.Setenv(llm.EnvNeuralDeepHubURL, hub.URL)

	home := t.TempDir()
	authPath := config.NeuralDeepAuthPath(home, "x")
	if err := llm.SaveNeuralDeepAuth(authPath, "key-1", hub.URL, "coddy", "bdd"); err != nil {
		t.Fatal(err)
	}
	srv := signInTestServer(t, home,
		config.ProviderConfig{Name: "x", Type: "neuraldeep"}, "")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodDelete,
		ts.URL+"/coddy/providers/x/neuraldeep-auth?proxy="+fmt.Sprintf("%%25"), nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusOK {
		t.Fatalf("an invalid ?proxy= must not be silently accepted")
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid ?proxy= must be a 400, got %d", res.StatusCode)
	}
}

// A device-start body value the form did not type falls back to the row's
// saved proxy - no override.
func neuralDeepSignInHub(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/cli/device/start":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code": "dc-1", "user_code": "ABCD-EFGH",
				"verification_uri": "https://hub/device", "interval": 0, "expires_in": 900,
			})
		case "/api/cli/device/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "k"})
		case "/api/cli/revoke":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
}
