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
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// neuralDeepBDDState drives features/neuraldeep_auth.feature: a stand-in hub
// (browser-callback start page, device flow, whoami/status/revoke) plus a
// stand-in OpenAI-compatible API that records the Authorization header.
type neuralDeepBDDState struct {
	home    string
	hub     *httptest.Server
	api     *httptest.Server
	server  *Server
	ts      *httptest.Server
	loginID string

	mu       sync.Mutex
	apiAuths []string

	prevHubEnv  string
	prevBaseEnv string
	prevKeyEnv  string
}

const neuralDeepBDDKey = "sk-bdd-tier-key"

func (s *neuralDeepBDDState) reset() error {
	s.home, _ = os.MkdirTemp("", "coddy-nd-bdd-*")
	s.apiAuths = nil
	s.loginID = ""

	s.hub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cli/auth/start":
			q := r.URL.Query()
			cb := fmt.Sprintf("http://127.0.0.1:%s/cb?state=%s&key=%s",
				q.Get("port"), url.QueryEscape(q.Get("state")), url.QueryEscape(neuralDeepBDDKey))
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			// Production answers with an HTML page whose script (and fallback
			// link) navigate to the loopback callback; the test browser follows
			// the link exactly like a user agent executing location.replace.
			_, _ = fmt.Fprintf(w, `<!doctype html><body><h2>ok</h2><a href="%s">continue</a><script>location.replace(%q)</script></body>`, cb, cb)
		case "/api/cli/device/start":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code": "dev-bdd", "user_code": "BDDX-CODE",
				"verification_uri":          s.hubURL() + "/app/device",
				"verification_uri_complete": s.hubURL() + "/app/device?code=BDDX-CODE",
				"interval":                  0, "expires_in": 900,
			})
		case "/api/cli/device/token":
			_, _ = fmt.Fprint(w, `{"access_token":"`+neuralDeepBDDKey+`","token_type":"bearer","label":"coddy @ bdd"}`)
		case "/api/cli/whoami":
			if r.Header.Get("Authorization") != "Bearer "+neuralDeepBDDKey {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"email": "bdd@example.com", "name": "bdd", "tier": "starter"})
		case "/api/cli/status":
			if r.Header.Get("Authorization") != "Bearer "+neuralDeepBDDKey {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tier": "starter",
				"models": []map[string]any{
					{"id": "qwen3.6-35b-a3b", "mode": "chat", "ctx": 262144},
					{"id": "gpt-oss-120b", "mode": "chat", "ctx": 131072},
				},
			})
		case "/api/cli/revoke":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))

	s.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		s.mu.Lock()
		s.apiAuths = append(s.apiAuths, r.Header.Get("Authorization"))
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[{"id":"qwen3.6-35b-a3b"},{"id":"gpt-oss-120b"}]}`)
	}))

	s.prevHubEnv = os.Getenv(llm.EnvNeuralDeepHubURL)
	s.prevBaseEnv = os.Getenv(llm.EnvNeuralDeepBaseURL)
	s.prevKeyEnv = os.Getenv("NEURALDEEP_API_KEY")
	if err := os.Setenv(llm.EnvNeuralDeepHubURL, s.hub.URL); err != nil {
		return err
	}
	// A real $CODDY_HOME/.env loaded elsewhere in this process must not leak
	// an explicit key into the credential-source assertions.
	if err := os.Setenv("NEURALDEEP_API_KEY", ""); err != nil {
		return err
	}
	return os.Setenv(llm.EnvNeuralDeepBaseURL, s.api.URL)
}

func (s *neuralDeepBDDState) hubURL() string {
	if s.hub == nil {
		return ""
	}
	return s.hub.URL
}

func (s *neuralDeepBDDState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.server != nil {
		s.server.Drain()
		s.server = nil
	}
	if s.hub != nil {
		s.hub.Close()
		s.hub = nil
	}
	if s.api != nil {
		s.api.Close()
		s.api = nil
	}
	_ = os.Setenv(llm.EnvNeuralDeepHubURL, s.prevHubEnv)
	_ = os.Setenv(llm.EnvNeuralDeepBaseURL, s.prevBaseEnv)
	_ = os.Setenv("NEURALDEEP_API_KEY", s.prevKeyEnv)
	if s.home != "" {
		_ = os.RemoveAll(s.home)
		s.home = ""
	}
}

func (s *neuralDeepBDDState) authPath() string {
	return config.NeuralDeepAuthPath(s.home, "neuraldeep")
}

// --- @cli scenario -----------------------------------------------------------

func (s *neuralDeepBDDState) givenHubAndAPI() error {
	// reset() already stood everything up; the step exists for readability.
	if s.hub == nil || s.api == nil {
		return fmt.Errorf("stand-ins are not running")
	}
	return nil
}

func (s *neuralDeepBDDState) signInWithBrowserCallback() error {
	browse := func(authURL string) error {
		resp, err := http.Get(authURL)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		m := regexp.MustCompile(`href="([^"]+)"`).FindSubmatch(body)
		if m == nil {
			return fmt.Errorf("no callback link in the hub page: %s", body)
		}
		cb, err := http.Get(string(m[1]))
		if err != nil {
			return err
		}
		_ = cb.Body.Close()
		if cb.StatusCode != http.StatusOK {
			return fmt.Errorf("callback status %d", cb.StatusCode)
		}
		return nil
	}
	errCh := make(chan error, 1)
	key, err := llm.NeuralDeepSignIn(context.Background(), s.hub.URL, s.hub.Client(), s.authPath(), func(p llm.NeuralDeepLoginPrompt) {
		go func() { errCh <- browse(p.AuthURL) }()
	})
	if err != nil {
		return err
	}
	if browseErr := <-errCh; browseErr != nil {
		return browseErr
	}
	if key != neuralDeepBDDKey {
		return fmt.Errorf("key = %q, want the hub-issued key", key)
	}
	// The CLI applies the login to config.yaml right after the greeting.
	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: s.home})
	if err != nil {
		return err
	}
	_, err = llm.ApplyNeuralDeepLoginToConfig(context.Background(), cfg, "neuraldeep", s.hub.URL, key, s.hub.Client())
	return err
}

func (s *neuralDeepBDDState) authFileHoldsKey() error {
	key, err := llm.LoadNeuralDeepKey(s.authPath())
	if err != nil {
		return err
	}
	if key != neuralDeepBDDKey {
		return fmt.Errorf("stored key = %q, want the hub-issued key", key)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(s.authPath())
		if err != nil {
			return err
		}
		if fi.Mode().Perm() != 0o600 {
			return fmt.Errorf("auth file mode = %v, want 0600", fi.Mode().Perm())
		}
	}
	return nil
}

func (s *neuralDeepBDDState) modelListUsesHubKey() error {
	if s.ts != nil {
		// @http scenario: through the running server's REST surface.
		res, err := http.Get(s.ts.URL + "/coddy/providers/neuraldeep/models")
		if err != nil {
			return err
		}
		defer func() { _ = res.Body.Close() }()
		var out struct {
			OK     bool `json:"ok"`
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			return err
		}
		if !out.OK || len(out.Models) != 2 {
			return fmt.Errorf("model list = %+v", out)
		}
	} else {
		// @cli scenario: the same resolver the CLI and agent use.
		models, err := llm.ListModels(context.Background(), llm.ProviderInput{
			Type:     "neuraldeep",
			AuthPath: s.authPath(),
		})
		if err != nil {
			return err
		}
		if len(models) != 2 {
			return fmt.Errorf("models = %+v, want the two tier models", models)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.apiAuths) == 0 {
		return fmt.Errorf("the API received no model-list request")
	}
	for i, got := range s.apiAuths {
		if got != "Bearer "+neuralDeepBDDKey {
			return fmt.Errorf("request %d Authorization = %q, want the hub key", i+1, got)
		}
	}
	return nil
}

func (s *neuralDeepBDDState) configGainedProviderAndModels() error {
	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: s.home})
	if err != nil {
		return err
	}
	prov := cfg.FindProvider("neuraldeep")
	if prov == nil || prov.Type != "neuraldeep" {
		return fmt.Errorf("neuraldeep provider missing from config: %+v", cfg.Providers)
	}
	for _, ref := range []string{"neuraldeep/qwen3.6-35b-a3b", "neuraldeep/gpt-oss-120b"} {
		if cfg.FindModelEntry(ref) == nil {
			return fmt.Errorf("model %s missing from config", ref)
		}
	}
	if !strings.HasPrefix(cfg.Agent.Model, "neuraldeep/") {
		return fmt.Errorf("agent.model = %q, want a neuraldeep default", cfg.Agent.Model)
	}
	return nil
}

// --- @http scenario ----------------------------------------------------------

func (s *neuralDeepBDDState) startServerWithProvider() error {
	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home},
		Providers: []config.ProviderConfig{{Name: "neuraldeep", Type: "neuraldeep"}},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	dir, err := os.MkdirTemp("", "coddy-nd-bdd-sessions-*")
	if err != nil {
		return err
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), dir, nil)
	s.server = New(cfg, mgr, slog.Default(), filepath.Dir(dir))
	s.ts = httptest.NewServer(s.server.Handler())
	return nil
}

func (s *neuralDeepBDDState) signInThroughRESTDeviceFlow() error {
	res, err := http.Post(s.ts.URL+"/coddy/providers/neuraldeep/neuraldeep-auth/device", "application/json", nil)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("device start status %d", res.StatusCode)
	}
	var start struct {
		LoginID         string `json:"login_id"`
		UserCode        string `json:"user_code"`
		VerificationURL string `json:"verification_url"`
	}
	if err := json.NewDecoder(res.Body).Decode(&start); err != nil {
		return err
	}
	if start.LoginID == "" || start.UserCode == "" || start.VerificationURL == "" {
		return fmt.Errorf("incomplete device start: %+v", start)
	}
	s.loginID = start.LoginID

	deadline := time.Now().Add(3 * time.Second)
	for {
		poll, err := http.Get(s.ts.URL + "/coddy/providers/neuraldeep/neuraldeep-auth/device/" + s.loginID)
		if err != nil {
			return err
		}
		var status struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.NewDecoder(poll.Body).Decode(&status); err != nil {
			_ = poll.Body.Close()
			return err
		}
		_ = poll.Body.Close()
		switch status.Status {
		case "completed":
			return nil
		case "failed":
			return fmt.Errorf("device login failed: %s", status.Error)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("device login did not complete, last status %q", status.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *neuralDeepBDDState) providerReportsConnectedMasked() error {
	res, err := http.Get(s.ts.URL + "/coddy/providers/neuraldeep/neuraldeep-auth")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var st struct {
		Connected bool   `json:"connected"`
		Masked    string `json:"masked"`
		Source    string `json:"source"`
	}
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		return err
	}
	if !st.Connected || st.Source != "oauth" {
		return fmt.Errorf("status = %+v, want a connected oauth login", st)
	}
	if st.Masked == "" || strings.Contains(st.Masked, neuralDeepBDDKey) {
		return fmt.Errorf("masked = %q must hide the key", st.Masked)
	}
	return nil
}

func (s *neuralDeepBDDState) signOutOverREST() error {
	req, err := http.NewRequest(http.MethodDelete, s.ts.URL+"/coddy/providers/neuraldeep/neuraldeep-auth", nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("sign-out status %d", res.StatusCode)
	}
	return nil
}

func (s *neuralDeepBDDState) providerReportsDisconnected() error {
	res, err := http.Get(s.ts.URL + "/coddy/providers/neuraldeep/neuraldeep-auth")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var st struct {
		Connected bool   `json:"connected"`
		Source    string `json:"source"`
	}
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		return err
	}
	if st.Connected || st.Source != "none" {
		return fmt.Errorf("status after sign-out = %+v, want disconnected", st)
	}
	return nil
}

func initializeNeuralDeepScenario(sc *godog.ScenarioContext) {
	s := &neuralDeepBDDState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a stand-in NeuralDeep hub and API for provider "neuraldeep"$`, s.givenHubAndAPI)
	sc.Step(`^I sign in to NeuralDeep with the browser callback flow$`, s.signInWithBrowserCallback)
	sc.Step(`^the neuraldeep auth file holds the hub key with private permissions$`, s.authFileHoldsKey)
	sc.Step(`^the provider model list is fetched with the hub key$`, s.modelListUsesHubKey)
	sc.Step(`^the config gains the neuraldeep provider and its tier models$`, s.configGainedProviderAndModels)

	sc.Step(`^a coddy HTTP server with a neuraldeep provider and a stand-in hub$`, s.startServerWithProvider)
	sc.Step(`^I sign in to NeuralDeep through the device flow over REST$`, s.signInThroughRESTDeviceFlow)
	sc.Step(`^the neuraldeep provider reports connected with a masked key$`, s.providerReportsConnectedMasked)
	sc.Step(`^I sign out of NeuralDeep over REST$`, s.signOutOverREST)
	sc.Step(`^the neuraldeep provider reports disconnected$`, s.providerReportsDisconnected)
}

func TestNeuralDeepAuthE2E(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "neuraldeep_auth",
		ScenarioInitializer: initializeNeuralDeepScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/neuraldeep_auth.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("neuraldeep_auth feature failed")
	}
}
