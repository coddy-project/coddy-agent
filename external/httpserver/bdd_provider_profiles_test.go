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
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// providerProfilesState drives features/provider_profiles.feature: several
// provider rows of one type on one server, with a Codex CLI login in
// CODEX_HOME, a stand-in ChatGPT issuer and Codex backend, and a stand-in
// NeuralDeep hub and API that tell the keys and labels of the rows apart.
type providerProfilesState struct {
	root string
	home string

	issuer        *codexIssuerStandIn
	codexBackend  *httptest.Server
	backendModels atomic.Int32

	hub *httptest.Server
	api *httptest.Server
	mu  sync.Mutex
	// labels is the device_label of every NeuralDeep device start, in order.
	labels []string
	// apiAuths is the Authorization of every NeuralDeep model list request.
	apiAuths []string

	server *Server
	ts     *httptest.Server
	env    map[string]*string
}

const providerProfilesCLIAccount = "acct-cli"

func (s *providerProfilesState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-profiles-*")
	if err != nil {
		return err
	}
	s.root = root
	s.home = filepath.Join(root, "home")
	s.labels, s.apiAuths = nil, nil
	s.backendModels.Store(0)
	return os.MkdirAll(s.home, 0o755)
}

// setEnv sets a variable for the scenario and remembers how to put it back.
func (s *providerProfilesState) setEnv(name, value string) error {
	if s.env == nil {
		s.env = map[string]*string{}
	}
	if _, saved := s.env[name]; !saved {
		if prev, ok := os.LookupEnv(name); ok {
			s.env[name] = &prev
		} else {
			s.env[name] = nil
		}
	}
	return os.Setenv(name, value)
}

func (s *providerProfilesState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.server != nil {
		s.server.Drain()
		s.server = nil
	}
	for _, srv := range []*httptest.Server{s.codexBackend, s.hub, s.api} {
		if srv != nil {
			srv.Close()
		}
	}
	s.codexBackend, s.hub, s.api = nil, nil, nil
	if s.issuer != nil {
		s.issuer.srv.Close()
		s.issuer = nil
	}
	for name, prev := range s.env {
		if prev == nil {
			_ = os.Unsetenv(name)
		} else {
			_ = os.Setenv(name, *prev)
		}
	}
	s.env = nil
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *providerProfilesState) startServer(providers []config.ProviderConfig) error {
	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home, ConfigPath: filepath.Join(s.home, "config.yaml")},
		Providers: providers,
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), filepath.Join(s.root, "sessions"), nil)
	s.server = New(cfg, mgr, slog.Default(), s.root)
	if s.issuer != nil {
		s.server.codexAuthIssuer = s.issuer.srv.URL
	}
	s.ts = httptest.NewServer(s.server.Handler())
	return nil
}

// --- Codex -----------------------------------------------------------------

func (s *providerProfilesState) startCodexProfiles(a, b, c string) error {
	codexHome := filepath.Join(s.root, "codex-cli")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	cliLogin, _ := json.Marshal(map[string]any{
		"auth_mode":      "chatgpt",
		"OPENAI_API_KEY": nil,
		"tokens": map[string]string{
			"id_token":      codexHTTPTestJWT(map[string]any{"chatgpt_account_id": providerProfilesCLIAccount}),
			"access_token":  codexHTTPTestJWT(map[string]any{"exp": 4_102_444_800}),
			"refresh_token": "refresh-cli",
			"account_id":    providerProfilesCLIAccount,
		},
		"last_refresh": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), cliLogin, 0o600); err != nil {
		return err
	}
	if err := s.setEnv("CODEX_HOME", codexHome); err != nil {
		return err
	}
	s.codexBackend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			s.backendModels.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"models":[{"slug":"gpt-5.5","display_name":"GPT-5.5"}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	if err := s.setEnv(llm.EnvCodexBaseURL, s.codexBackend.URL); err != nil {
		return err
	}
	s.issuer = startCodexIssuerStandIn(false)
	close(s.issuer.confirm)
	return s.startServer([]config.ProviderConfig{
		{Name: a, Type: "codex"}, {Name: b, Type: "codex"}, {Name: c, Type: "codex"},
	})
}

type codexProfileStatus struct {
	Connected bool   `json:"connected"`
	Source    string `json:"source"`
	AccountID string `json:"account_id"`
	// CLILoginRow names the row the Codex CLI login serves when this row
	// may not use it.
	CLILoginRow string `json:"cli_login_row"`
}

func (s *providerProfilesState) codexStatus(row string) (codexProfileStatus, error) {
	var st codexProfileStatus
	res, err := http.Get(s.ts.URL + "/coddy/providers/" + row + "/codex-auth")
	if err != nil {
		return st, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return st, fmt.Errorf("codex-auth status of %s: HTTP %d", row, res.StatusCode)
	}
	return st, json.NewDecoder(res.Body).Decode(&st)
}

func (s *providerProfilesState) rowSignedInThroughCLILogin(row string) error {
	st, err := s.codexStatus(row)
	if err != nil {
		return err
	}
	if !st.Connected || st.Source != "codex_cli" || st.AccountID != providerProfilesCLIAccount {
		return fmt.Errorf("row %s = %+v, want connected through the Codex CLI login", row, st)
	}
	return nil
}

func (s *providerProfilesState) rowNotSignedInNamingCLIRow(row, owner string) error {
	st, err := s.codexStatus(row)
	if err != nil {
		return err
	}
	if st.Connected || st.Source != "" || st.AccountID != "" {
		return fmt.Errorf("row %s = %+v, want not signed in: the Codex CLI login is another row's", row, st)
	}
	if st.CLILoginRow != owner {
		return fmt.Errorf("row %s names %q as the row the Codex CLI login serves, want %q", row, st.CLILoginRow, owner)
	}
	return nil
}

func (s *providerProfilesState) rowListsNoModels(row string) error {
	before := s.backendModels.Load()
	res, err := http.Get(s.ts.URL + "/coddy/providers/" + row + "/models")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var out struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Models []any  `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return err
	}
	if out.OK || len(out.Models) != 0 {
		return fmt.Errorf("row %s listed %d models, want none without a login of its own", row, len(out.Models))
	}
	if after := s.backendModels.Load(); after != before {
		return fmt.Errorf("row %s reached the Codex backend with another row's login", row)
	}
	return nil
}

func (s *providerProfilesState) signInCodexRow(row string) error {
	res, err := http.Post(s.ts.URL+"/coddy/providers/"+row+"/codex-auth/device", "application/json", strings.NewReader("{}"))
	if err != nil {
		return err
	}
	var start codexAuthLoginResponse
	err = json.NewDecoder(res.Body).Decode(&start)
	_ = res.Body.Close()
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK || start.LoginID == "" {
		return fmt.Errorf("device start for %s: HTTP %d %+v", row, res.StatusCode, start)
	}
	return s.waitLogin("/coddy/providers/" + row + "/codex-auth/device/" + start.LoginID)
}

func (s *providerProfilesState) waitLogin(path string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, err := http.Get(s.ts.URL + path)
		if err != nil {
			return err
		}
		var st codexAuthLoginResponse
		err = json.NewDecoder(res.Body).Decode(&st)
		_ = res.Body.Close()
		if err != nil {
			return err
		}
		switch st.Status {
		case "completed":
			return nil
		case "failed":
			return fmt.Errorf("login %s failed: %s", path, st.Error)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("login %s did not complete, last status %q", path, st.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *providerProfilesState) rowSignedInWithOwnAccount(row string) error {
	st, err := s.codexStatus(row)
	if err != nil {
		return err
	}
	if !st.Connected || st.Source != "coddy" || st.AccountID == "" || st.AccountID == providerProfilesCLIAccount {
		return fmt.Errorf("row %s = %+v, want connected with a Coddy-managed login of its own", row, st)
	}
	return nil
}

func (s *providerProfilesState) rowStillNotSignedIn(row string) error {
	st, err := s.codexStatus(row)
	if err != nil {
		return err
	}
	if st.Connected {
		return fmt.Errorf("row %s = %+v, want still not signed in", row, st)
	}
	return nil
}

// --- NeuralDeep -------------------------------------------------------------

func (s *providerProfilesState) startNeuralDeepProfiles(a, b string) error {
	var starts atomic.Int32
	s.hub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cli/device/start":
			var body struct {
				DeviceLabel string `json:"device_label"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.mu.Lock()
			s.labels = append(s.labels, body.DeviceLabel)
			s.mu.Unlock()
			n := starts.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code": fmt.Sprintf("dev-%d", n), "user_code": fmt.Sprintf("PROF-%04d", n),
				"verification_uri": "http://hub/app/device", "interval": 0, "expires_in": 900,
			})
		case "/api/cli/device/token":
			var body struct {
				DeviceCode string `json:"device_code"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_, _ = fmt.Fprintf(w, `{"access_token":"sk-nd-%s","token_type":"bearer"}`, body.DeviceCode)
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
		_, _ = fmt.Fprint(w, `{"data":[{"id":"qwen3.6-35b-a3b"}]}`)
	}))
	for name, value := range map[string]string{
		llm.EnvNeuralDeepHubURL:            s.hub.URL,
		llm.EnvNeuralDeepBaseURL:           s.api.URL,
		config.ProviderAPIKeyEnvVarName(a): "",
		config.ProviderAPIKeyEnvVarName(b): "",
	} {
		if err := s.setEnv(name, value); err != nil {
			return err
		}
	}
	return s.startServer([]config.ProviderConfig{{Name: a, Type: "neuraldeep"}, {Name: b, Type: "neuraldeep"}})
}

func (s *providerProfilesState) signInNeuralDeepRow(row string) error {
	res, err := http.Post(s.ts.URL+"/coddy/providers/"+row+"/neuraldeep-auth/device", "application/json", strings.NewReader("{}"))
	if err != nil {
		return err
	}
	var start codexAuthLoginResponse
	err = json.NewDecoder(res.Body).Decode(&start)
	_ = res.Body.Close()
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK || start.LoginID == "" {
		return fmt.Errorf("device start for %s: HTTP %d %+v", row, res.StatusCode, start)
	}
	return s.waitLogin("/coddy/providers/" + row + "/neuraldeep-auth/device/" + start.LoginID)
}

func (s *providerProfilesState) neuralDeepRowsListWithOwnKeys() error {
	seen := map[string]string{}
	for _, row := range []string{"neuraldeep", "nd-tech"} {
		key, err := llm.LoadNeuralDeepKey(config.NeuralDeepAuthPath(s.home, row))
		if err != nil {
			return fmt.Errorf("row %s: %w", row, err)
		}
		if other, dup := seen[key]; dup {
			return fmt.Errorf("rows %s and %s hold the same key", other, row)
		}
		seen[key] = row
		res, err := http.Get(s.ts.URL + "/coddy/providers/" + row + "/models")
		if err != nil {
			return err
		}
		var out struct {
			OK bool `json:"ok"`
		}
		err = json.NewDecoder(res.Body).Decode(&out)
		_ = res.Body.Close()
		if err != nil {
			return err
		}
		s.mu.Lock()
		last := ""
		if n := len(s.apiAuths); n > 0 {
			last = s.apiAuths[n-1]
		}
		s.mu.Unlock()
		if !out.OK || last != "Bearer "+key {
			return fmt.Errorf("row %s listed models ok=%v with %q, want its own key", row, out.OK, last)
		}
	}
	return nil
}

func (s *providerProfilesState) hubLabelledKeyWithRow(row string) error {
	s.mu.Lock()
	labels := append([]string(nil), s.labels...)
	s.mu.Unlock()
	if len(labels) != 2 {
		return fmt.Errorf("device labels = %q, want one per sign-in", labels)
	}
	if !strings.HasSuffix(labels[1], "("+row+")") {
		return fmt.Errorf("label of %s = %q, want it to end with (%s)", row, labels[1], row)
	}
	if labels[0] == labels[1] {
		return fmt.Errorf("both rows reached the hub with the label %q", labels[0])
	}
	st, err := llm.InspectNeuralDeepAuth(config.NeuralDeepAuthPath(s.home, row))
	if err != nil {
		return err
	}
	if st.KeyName != labels[1] {
		return fmt.Errorf("stored key name of %s = %q, want the label %q", row, st.KeyName, labels[1])
	}
	return nil
}

func initializeProviderProfilesScenario(sc *godog.ScenarioContext) {
	s := &providerProfilesState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy HTTP server with codex rows "([^"]+)", "([^"]+)" and "([^"]+)" and a Codex CLI login$`, s.startCodexProfiles)
	sc.Step(`^the row "([^"]+)" is signed in through the Codex CLI login$`, s.rowSignedInThroughCLILogin)
	sc.Step(`^the row "([^"]+)" is not signed in and names "([^"]+)" as the row the Codex CLI login serves$`, s.rowNotSignedInNamingCLIRow)
	sc.Step(`^the row "([^"]+)" lists no models without reaching the Codex backend$`, s.rowListsNoModels)
	sc.Step(`^I sign in the row "([^"]+)" through the device flow over REST$`, s.signInCodexRow)
	sc.Step(`^the row "([^"]+)" is signed in with an account of its own$`, s.rowSignedInWithOwnAccount)
	sc.Step(`^the row "([^"]+)" is still not signed in$`, s.rowStillNotSignedIn)

	sc.Step(`^a coddy HTTP server with neuraldeep rows "([^"]+)" and "([^"]+)" and a stand-in hub$`, s.startNeuralDeepProfiles)
	sc.Step(`^I sign in the row "([^"]+)" through the NeuralDeep device flow over REST$`, s.signInNeuralDeepRow)
	sc.Step(`^each neuraldeep row lists its models with the key the hub minted for it$`, s.neuralDeepRowsListWithOwnKeys)
	sc.Step(`^the hub labelled the key of "([^"]+)" with the row name$`, s.hubLabelledKeyWithRow)
}

func TestProviderProfilesE2E(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "provider_profiles",
		ScenarioInitializer: initializeProviderProfilesScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/provider_profiles.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("provider_profiles feature failed")
	}
}
