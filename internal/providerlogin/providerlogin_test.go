package providerlogin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// isolate points every credential a test could pick up from the machine at a
// temporary home: no Codex or Devin CLI login, no provider key in the
// environment.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CODEX_HOME", filepath.Join(dir, ".codex"))
	t.Setenv(llm.EnvDevinCLICredentials, filepath.Join(dir, "no-devin-cli.toml"))
	for _, name := range []string{"OPENAI_API_KEY", "NEURALDEEP_API_KEY", "CODEX_API_KEY", "DEVIN_API_KEY", "OPENCODE_API_KEY", "LOCAL_API_KEY"} {
		t.Setenv(name, "")
	}
	return dir
}

// freshConfig is the configuration of an install that has no config.yaml
// yet: its paths, nothing else.
func freshConfig(home string) *config.Config {
	return &config.Config{Paths: config.Paths{
		Home:       home,
		CWD:        home,
		ConfigPath: filepath.Join(home, "config.yaml"),
	}}
}

func TestResolve(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{
		{Name: "codex", Type: "codex"},
		{Name: "mine", Type: "openai"},
	}}
	if prov, err := Resolve(cfg, "codex", ""); err != nil || prov != &cfg.Providers[0] {
		t.Fatalf("a configured row resolves to itself: %v %v", prov, err)
	}
	if prov, err := Resolve(cfg, "mine", ""); err != nil || prov.Type != "openai" {
		t.Fatalf("a configured row of any type resolves, the caller decides: %v %v", prov, err)
	}
	if _, err := Resolve(cfg, "mine", "codex"); err == nil || !strings.Contains(err.Error(), `type "openai"`) {
		t.Fatalf("a type that contradicts the row is refused: %v", err)
	}
	for _, typ := range Types {
		prov, err := Resolve(&config.Config{}, typ, "")
		if err != nil || prov.Name != typ || prov.Type != typ {
			t.Fatalf("the name %q is its own type on a fresh install: %v %v", typ, prov, err)
		}
	}
	if prov, err := Resolve(cfg, "codex-work", "codex"); err != nil || prov.Type != "codex" || prov.Name != "codex-work" {
		t.Fatalf("another profile of a type signs in under its own name: %v %v", prov, err)
	}
	var notConfigured *NotConfiguredError
	if _, err := Resolve(cfg, "work", ""); !errors.As(err, &notConfigured) || notConfigured.Name != "work" {
		t.Fatalf("an unknown name with no type is NotConfiguredError: %v", err)
	}
	var unsupported *UnsupportedTypeError
	if _, err := Resolve(cfg, "x", "openai"); !errors.As(err, &unsupported) || unsupported.Type != "openai" {
		t.Fatalf("a type with no sign-in is UnsupportedTypeError: %v", err)
	}
}

func TestDefaultTypeIsNeuralDeep(t *testing.T) {
	if DefaultType != "neuraldeep" || Types[0] != DefaultType {
		t.Fatalf("a bare /login signs in to NeuralDeep, got %q (types %v)", DefaultType, Types)
	}
	for _, typ := range []string{"neuraldeep", "codex", "devin", "opencode"} {
		if !Supports(typ) {
			t.Fatalf("%s signs in without a key", typ)
		}
	}
	for _, typ := range []string{"openai", "anthropic", "acp", ""} {
		if Supports(typ) {
			t.Fatalf("%q has no sign-in", typ)
		}
	}
}

func TestReady(t *testing.T) {
	home := isolate(t)
	ndHome := t.TempDir()
	if err := llm.SaveNeuralDeepAuth(config.NeuralDeepAuthPath(ndHome, "neuraldeep"), "sk-stored-0123456789abcdef", "https://hub.invalid", llm.NeuralDeepClientID, "coddy"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		home string
		rows []config.ProviderConfig
		env  map[string]string
		want bool
	}{
		{name: "no config.yaml at all", home: home, want: false},
		{name: "the example config with OPENAI_API_KEY unset", home: home,
			rows: []config.ProviderConfig{{Name: "openai", Type: "openai"}}, want: false},
		{name: "a key in config.yaml", home: home,
			rows: []config.ProviderConfig{{Name: "openai", Type: "openai", APIKey: "sk-x"}}, want: true},
		{name: "a credential helper", home: home,
			rows: []config.ProviderConfig{{Name: "openai", Type: "openai", APIKeyCommand: "pass show openai"}}, want: true},
		{name: "the NAME_API_KEY variable", home: home,
			rows: []config.ProviderConfig{{Name: "openai", Type: "openai"}},
			env:  map[string]string{"OPENAI_API_KEY": "sk-env"}, want: true},
		{name: "a local OpenAI-compatible server needs no key", home: home,
			rows: []config.ProviderConfig{{Name: "local", Type: "openai", APIBase: "http://127.0.0.1:11434/v1"}}, want: true},
		{name: "anthropic without a key", home: home,
			rows: []config.ProviderConfig{{Name: "claude", Type: "anthropic"}}, want: false},
		{name: "an acp agent signs in on its own", home: home,
			rows: []config.ProviderConfig{{Name: "claude", Type: "acp", ACP: &config.ACPProviderConfig{Command: "claude-agent-acp"}}}, want: true},
		{name: "a neuraldeep row nobody signed in", home: home,
			rows: []config.ProviderConfig{{Name: "neuraldeep", Type: "neuraldeep"}}, want: false},
		{name: "a neuraldeep row with its stored sign-in", home: ndHome,
			rows: []config.ProviderConfig{{Name: "neuraldeep", Type: "neuraldeep"}}, want: true},
		{name: "a codex row nobody signed in", home: home,
			rows: []config.ProviderConfig{{Name: "codex", Type: "codex"}}, want: false},
		{name: "one usable row among others is enough", home: ndHome,
			rows: []config.ProviderConfig{{Name: "openai", Type: "openai"}, {Name: "neuraldeep", Type: "neuraldeep"}}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg := freshConfig(tc.home)
			cfg.Providers = tc.rows
			if got := Ready(cfg); got != tc.want {
				t.Fatalf("Ready = %v, want %v", got, tc.want)
			}
		})
	}
	if Ready(nil) {
		t.Fatal("no configuration is not ready")
	}
}

func TestSummarizeAdded(t *testing.T) {
	short := []string{"provider codex", "model codex/a", "model codex/b", "agent.model codex/a"}
	if got := SummarizeAdded(short); got != "provider codex, agent.model codex/a, model codex/a, model codex/b" {
		t.Fatalf("a short list is spelled out: %q", got)
	}
	long := []string{"provider nd"}
	for _, m := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		long = append(long, "model nd/"+m)
	}
	if got := SummarizeAdded(long); got != "provider nd, 7 models (nd/a, nd/b, nd/c, ...)" {
		t.Fatalf("a long model list folds into a count: %q", got)
	}
}

// neuralDeepHub stands in for the NeuralDeep hub: the device flow answers at
// once, whoami names the account, status serves the tier's two chat models.
func neuralDeepHub(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/api/cli/device/start", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code": "dc-secret", "user_code": "WXYZ-2345",
			"verification_uri":          srv.URL + "/device",
			"verification_uri_complete": srv.URL + "/device?code=WXYZ-2345",
			"interval":                  1, "expires_in": 900,
		})
	})
	mux.HandleFunc("/api/cli/device/token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "sk-from-the-device-flow", "token_type": "bearer"})
	})
	mux.HandleFunc("/api/cli/whoami", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"email": "dev@example.test", "name": "dev", "tier": "pro"})
	})
	mux.HandleFunc("/api/cli/status", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tier": "pro", "models": []map[string]any{
			{"id": "qwen3.8-27b", "mode": "chat", "ctx": 262144},
			{"id": "gpt-oss-120b", "mode": "chat", "ctx": 131072},
		}})
	})
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.URL.Path)
		mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv(llm.EnvNeuralDeepHubURL, srv.URL)
	t.Cleanup(llm.SetNeuralDeepLoginTimeout(20 * time.Second))
	return srv, &calls
}

func TestRunNeuralDeepAddsEveryModel(t *testing.T) {
	home := isolate(t)
	hub, _ := neuralDeepHub(t)
	cfg := freshConfig(home)
	prov, err := Resolve(cfg, DefaultType, "")
	if err != nil {
		t.Fatal(err)
	}
	var prompts []Prompt
	res, err := Run(context.Background(), cfg, prov, Hooks{OnPrompt: func(p Prompt) { prompts = append(prompts, p) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 || prompts[0].Code != "WXYZ-2345" || !strings.HasPrefix(prompts[0].URL, hub.URL+"/device") || prompts[0].Pasteable {
		t.Fatalf("the device flow asks to confirm its code on the hub's page: %+v", prompts)
	}
	if res.Provider != "neuraldeep" || res.Type != "neuraldeep" || res.ConfigErr != nil {
		t.Fatalf("result: %+v", res)
	}
	if !strings.Contains(res.Account, "dev") || !strings.Contains(res.Account, "pro") {
		t.Fatalf("the account names who signed in and the tier: %q", res.Account)
	}
	if key, err := llm.LoadNeuralDeepKey(res.AuthPath); err != nil || key != "sk-from-the-device-flow" {
		t.Fatalf("the key the device flow issued is stored: %q %v", key, err)
	}
	got, err := config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if got.FindProvider("neuraldeep") == nil {
		t.Fatal("config.yaml gains the neuraldeep provider")
	}
	for _, ref := range []string{"neuraldeep/qwen3.8-27b", "neuraldeep/gpt-oss-120b"} {
		if got.FindModelEntry(ref) == nil {
			t.Fatalf("config.yaml lists every model of the tier, %s missing", ref)
		}
	}
	if got.Agent.Model != "neuraldeep/qwen3.8-27b" {
		t.Fatalf("an install with no model takes the first one: %q", got.Agent.Model)
	}
	if !slices.Contains(res.Added, "model neuraldeep/gpt-oss-120b") {
		t.Fatalf("the result names what was added: %v", res.Added)
	}
	if !Ready(got) {
		t.Fatal("after the sign-in the configuration has a provider that can answer")
	}
}

func TestRunStopsWhenCancelled(t *testing.T) {
	home := isolate(t)
	// The hub never confirms: the sign-in waits until the context ends.
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cli/device/start":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code": "dc", "user_code": "AAAA-BBBB", "verification_uri": srv.URL + "/device",
				"interval": 1, "expires_in": 900,
			})
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"authorization_pending"}`)
		}
	}))
	defer srv.Close()
	t.Setenv(llm.EnvNeuralDeepHubURL, srv.URL)
	cfg := freshConfig(home)
	prov, _ := Resolve(cfg, "neuraldeep", "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, cfg, prov, Hooks{OnPrompt: func(Prompt) { cancel() }})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("a cancelled sign-in reports the cancellation: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the sign-in did not stop with its context")
	}
	if got, err := config.LoadWithPaths(cfg.Paths); err == nil && len(got.Providers) > 0 {
		t.Fatal("a cancelled sign-in writes nothing into config.yaml")
	}
}

func testJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(claims)
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func TestRunCodexAddsEveryModel(t *testing.T) {
	home := isolate(t)
	access := testJWT(map[string]any{"exp": 4_102_444_800})
	idToken := testJWT(map[string]any{"chatgpt_account_id": "acct-login"})
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_, _ = io.WriteString(w, `{"device_auth_id":"device-1","user_code":"CODE-1234","interval":"0"}`)
		case "/api/accounts/deviceauth/token":
			_, _ = io.WriteString(w, `{"authorization_code":"code-1","code_challenge":"challenge-1","code_verifier":"verifier-1"}`)
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"id_token": idToken, "access_token": access, "refresh_token": "refresh-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"models":[
			{"slug":"gpt-5.5","display_name":"GPT-5.5","visibility":"list","priority":1},
			{"slug":"gpt-5.4","display_name":"GPT-5.4","visibility":"list","priority":2},
			{"slug":"gpt-reserve","visibility":"hide","priority":3}]}`)
	}))
	defer backend.Close()
	t.Setenv(llm.EnvCodexBaseURL, backend.URL)

	cfg := freshConfig(home)
	prov, err := Resolve(cfg, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	var prompt Prompt
	res, err := Run(context.Background(), cfg, prov, Hooks{CodexIssuer: issuer.URL, OnPrompt: func(p Prompt) { prompt = p }})
	if err != nil {
		t.Fatal(err)
	}
	if prompt.Code != "CODE-1234" || prompt.URL == "" {
		t.Fatalf("the device flow asks to enter its code: %+v", prompt)
	}
	if res.ConfigErr != nil || !strings.Contains(res.Account, "acct-login") {
		t.Fatalf("result: %+v", res)
	}
	got, err := config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"codex/gpt-5.5", "codex/gpt-5.4"} {
		if got.FindModelEntry(ref) == nil {
			t.Fatalf("config.yaml lists every model the subscription offers, %s missing", ref)
		}
	}
	if got.FindModelEntry("codex/gpt-reserve") != nil {
		t.Fatal("a model Codex hides stays out")
	}
	if got.Agent.Model != "codex/gpt-5.5" {
		t.Fatalf("agent.model is the model Codex ranks first: %q", got.Agent.Model)
	}
}

func TestRunRefusesATypeWithoutSignIn(t *testing.T) {
	cfg := freshConfig(isolate(t))
	_, err := Run(context.Background(), cfg, &config.ProviderConfig{Name: "mine", Type: "openai"}, Hooks{})
	var unsupported *UnsupportedTypeError
	if !errors.As(err, &unsupported) {
		t.Fatalf("an openai row has no sign-in: %v", err)
	}
}

func TestRunNamesAnExplicitKeyThatWinsOverTheLogin(t *testing.T) {
	home := isolate(t)
	neuralDeepHub(t)
	cfg := freshConfig(home)
	prov := &config.ProviderConfig{Name: "neuraldeep", Type: "neuraldeep", APIKey: "sk-in-config"}
	res, err := Run(context.Background(), cfg, prov, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Override != "api_key from config.yaml" {
		t.Fatalf("the row's own key wins over the login and the result says so: %q", res.Override)
	}
}

// install.sh writes the example config: an openai row reading an unset
// OPENAI_API_KEY and agent.model on it. A sign-in moves agent.model to the
// provider it signed in, so a new session starts on a model that answers;
// an agent.model that works stays.
func TestRunRepointsAnAgentModelThatCannotAnswer(t *testing.T) {
	home := isolate(t)
	neuralDeepHub(t)
	paths := freshConfig(home).Paths
	example := "providers:\n  - name: openai\n    type: openai\n    api_key: \"${OPENAI_API_KEY}\"\nmodels:\n  - model: openai/gpt-5.6-terra\nagent:\n  model: openai/gpt-5.6-terra\n"
	if err := os.WriteFile(paths.ConfigPath, []byte(example), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	prov, _ := Resolve(cfg, DefaultType, "")
	res, err := Run(context.Background(), cfg, prov, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if res.AgentModelFrom != "openai/gpt-5.6-terra" || !slices.Contains(res.Added, "agent.model neuraldeep/qwen3.8-27b") {
		t.Fatalf("result: %+v", res)
	}
	got, err := config.LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent.Model != "neuraldeep/qwen3.8-27b" {
		t.Fatalf("agent.model = %q, want the signed-in provider's first model", got.Agent.Model)
	}
	if got.FindModelEntry("openai/gpt-5.6-terra") == nil {
		t.Fatal("the openai model the operator listed stays in config.yaml")
	}

	// With the key set the openai row answers, and agent.model stays.
	home = isolate(t)
	t.Setenv("OPENAI_API_KEY", "sk-env")
	paths = freshConfig(home).Paths
	if err := os.WriteFile(paths.ConfigPath, []byte(example), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	prov, _ = Resolve(cfg, DefaultType, "")
	if res, err = Run(context.Background(), cfg, prov, Hooks{}); err != nil {
		t.Fatal(err)
	}
	if res.AgentModelFrom != "" {
		t.Fatalf("a working agent.model was replaced: %+v", res)
	}
	if got, _ = config.LoadWithPaths(paths); got.Agent.Model != "openai/gpt-5.6-terra" {
		t.Fatalf("agent.model = %q, want it untouched", got.Agent.Model)
	}
}

func TestDefaultModelAndProviderReady(t *testing.T) {
	isolate(t)
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "openai", Type: "openai"}, {Name: "local", Type: "openai", APIBase: "http://127.0.0.1:8080/v1"}},
		Models:    []config.ModelEntry{{Model: "openai/a"}, {Model: "local/b"}, {Model: "local/c"}},
		Agent:     config.Agent{Model: "local/c"},
	}
	if ProviderReady(cfg, "openai") || !ProviderReady(cfg, "local") || ProviderReady(cfg, "missing") {
		t.Fatal("ProviderReady decides one row the way Ready decides them all")
	}
	if got := DefaultModel(cfg, "local"); got != "local/c" {
		t.Fatalf("agent.model on the provider wins: %q", got)
	}
	if got := DefaultModel(cfg, "openai"); got != "openai/a" {
		t.Fatalf("else the provider's first model: %q", got)
	}
	if got := DefaultModel(cfg, "none"); got != "" {
		t.Fatalf("a provider with no model has none: %q", got)
	}
}
