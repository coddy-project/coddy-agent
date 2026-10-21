package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// opencodeConfigTestHome writes a config.yaml into a fresh CODDY_HOME and
// returns the loaded config.
func opencodeConfigTestHome(t *testing.T, yaml string) *config.Config {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: home})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

// opencodeConfigTestCatalog serves one catalog from a stand-in gateway and
// points the process at it, so ApplyOpenCodeLoginToConfig has models to publish.
func opencodeConfigTestCatalog(t *testing.T, catalog string) {
	t.Helper()
	stand := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, catalog)
	}))
	t.Cleanup(stand.Close)
	t.Setenv(EnvOpenCodeBaseURL, stand.URL)
}

// TestOpenCodeProtocolIgnoresTheStandOverride: Go and Console serve some ids
// over different protocols, and the protocol follows the row's gateway even
// while a stand points the traffic of the process elsewhere.
func TestOpenCodeProtocolIgnoresTheStandOverride(t *testing.T) {
	t.Setenv(EnvOpenCodeBaseURL, "https://stand.invalid/inference/go/v1")
	if got := opencodeProtocolForGateway("qwen3.8-max", opencodeConsoleBaseURL); got != opencodeChat {
		t.Errorf("Console qwen3.8-max = %v, want Chat Completions", got)
	}
	if got := opencodeProtocolForGateway("qwen3.8-max", ""); got != opencodeMessages {
		t.Errorf("Go qwen3.8-max = %v, want Messages", got)
	}
	if got := opencodeAPIBase(opencodeConsoleBaseURL); got != "https://stand.invalid/inference/go/v1" {
		t.Errorf("opencodeAPIBase = %q, want the stand", got)
	}
}

// TestOpenCodeClientRejectsANilClient: the row's proxy route lives in the
// transport handed in. A nil client must be refused rather than passed on: the
// SDKs then fall back to their own default, which is http.DefaultClient's
// route - the environment's proxy, no stall guard and no liveness pings.
func TestOpenCodeClientRejectsANilClient(t *testing.T) {
	if _, err := opencodeClient(nil, "k", "s", ""); err == nil {
		t.Error("opencodeClient(nil) = nil error, want a refusal")
	}
	if _, err := newOpenCodeProvider(ProviderInput{Type: "opencode", Model: "glm-5.3"}, nil); err == nil {
		t.Error("newOpenCodeProvider(nil client) = nil error, want a refusal")
	}
}

// TestApplyOpenCodeLoginToConfigDefaultIsAUsableModel: agent.model has to be a
// ref the same login wrote into models[]. The catalog is the gateway's, and an
// id outside the safe alphabet is skipped when the models are staged, so it
// must be skipped when the default is chosen as well - otherwise the config
// names a model nothing knows, with characters the config paths never see.
func TestApplyOpenCodeLoginToConfigDefaultIsAUsableModel(t *testing.T) {
	// "bad id].x" sorts first and fails catalogModelIDRe; "glm-5.3" is the
	// first id the login can actually stage.
	opencodeConfigTestCatalog(t, `{"data":[{"id":"glm-5.3","context_window":200000},{"id":"bad id].x"},{"id":"grok-4"}]}`)
	cfg := opencodeConfigTestHome(t, "providers: []\nmodels: []\n")

	added, err := ApplyOpenCodeLoginToConfig(context.Background(), cfg, "opencode", opencodeConsoleBaseURL, "oc-key", "")
	if err != nil {
		t.Fatalf("ApplyOpenCodeLoginToConfig: %v", err)
	}
	reloaded, err := config.LoadFromCLI(config.CLIPaths{Home: cfg.Paths.Home})
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := reloaded.Agent.Model; got != "opencode/glm-5.3" {
		t.Errorf("agent.model = %q, want opencode/glm-5.3 (added %v)", got, added)
	}
	if reloaded.FindModelEntry("opencode/bad id].x") != nil {
		t.Error("a catalog id outside the safe alphabet must not be staged into models[]")
	}
	for _, want := range []string{"opencode/glm-5.3", "opencode/grok-4"} {
		if reloaded.FindModelEntry(want) == nil {
			t.Errorf("config is missing model %q; added %v", want, added)
		}
	}
}

// TestApplyOpenCodeLoginToConfigNoDefaultWithoutAUsableModel: when every
// catalog id is refused there is no default to write, rather than a ref to a
// model the login never added.
func TestApplyOpenCodeLoginToConfigNoDefaultWithoutAUsableModel(t *testing.T) {
	opencodeConfigTestCatalog(t, `{"data":[{"id":"bad id].x"}]}`)
	cfg := opencodeConfigTestHome(t, "providers: []\nmodels: []\n")

	added, err := ApplyOpenCodeLoginToConfig(context.Background(), cfg, "opencode", opencodeConsoleBaseURL, "oc-key", "")
	if err != nil {
		t.Fatalf("ApplyOpenCodeLoginToConfig: %v", err)
	}
	for _, a := range added {
		if strings.HasPrefix(a, "agent.model ") {
			t.Errorf("no model was staged, so no default may be written: %v", added)
		}
	}
	reloaded, err := config.LoadFromCLI(config.CLIPaths{Home: cfg.Paths.Home})
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := reloaded.Agent.Model; got != "" {
		t.Errorf("agent.model = %q, want empty", got)
	}
}

// TestApplyOpenCodeLoginToConfigKeepsWhatTheOperatorAlreadyChose: the login
// publishes the catalog, it does not take the config over.
func TestApplyOpenCodeLoginToConfigKeepsWhatTheOperatorAlreadyChose(t *testing.T) {
	opencodeConfigTestCatalog(t, `{"data":[{"id":"glm-5.3"},{"id":"grok-4"}]}`)
	cfg := opencodeConfigTestHome(t, "providers:\n  - name: opencode\n    type: opencode\n"+
		"models:\n  - model: opencode/glm-5.3\n    max_tokens: 4242\n"+
		"agent:\n  model: opencode/glm-5.3\n")

	added, err := ApplyOpenCodeLoginToConfig(context.Background(), cfg, "opencode", opencodeGoBaseURL, "oc-key", "")
	if err != nil {
		t.Fatalf("ApplyOpenCodeLoginToConfig: %v", err)
	}
	for _, a := range added {
		if a == "provider opencode" {
			t.Errorf("an existing provider row must not be re-added: %v", added)
		}
		if strings.HasPrefix(a, "agent.model ") {
			t.Errorf("a chosen agent.model must be left alone: %v", added)
		}
	}
	reloaded, err := config.LoadFromCLI(config.CLIPaths{Home: cfg.Paths.Home})
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.Agent.Model != "opencode/glm-5.3" {
		t.Errorf("agent.model = %q, want the operator's choice opencode/glm-5.3", reloaded.Agent.Model)
	}
	kept := reloaded.FindModelEntry("opencode/glm-5.3")
	if kept == nil || kept.MaxTokens != 4242 {
		t.Errorf("the existing model entry lost its settings: %+v", kept)
	}
}

// TestOpenCodeLoginServesTheGatewayOfTheRow: as in the OpenCode client, where
// Go and Console are two providers on one OPENCODE_API_KEY, a login belongs
// to the account and not to a gateway. The row's api_base alone says where its
// requests go - completions, the catalog and the label of an error alike - so
// a login made on Console serves a row left on Go, and the gateway picker in
// Settings shows where the requests really go.
func TestOpenCodeLoginServesTheGatewayOfTheRow(t *testing.T) {
	t.Setenv(EnvOpenCodeBaseURL, "")
	var mu sync.Mutex
	var paths []string
	proxy := newProviderHTTPSProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer stored-key" {
			t.Errorf("%s: the stored login was not used", r.URL.Path)
		}
		if !answerProviderRequest(t, w, r) {
			http.NotFound(w, r)
		}
	}))
	path := filepath.Join(t.TempDir(), "opencode-auth.json")
	// A login file that names the gateway it was made on, as the earlier
	// builds of this provider wrote it.
	if err := os.WriteFile(path, []byte(`{"api_key":"stored-key","api_base":"`+opencodeConsoleBaseURL+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		apiBase, chat, catalog, label string
	}{
		{"", "/inference/go/openai/v1/chat/completions", "/inference/go/v1/models", opencodeGoBaseURL},
		{opencodeConsoleBaseURL, "/inference/openai/v1/chat/completions", "/inference/v1/models", opencodeConsoleBaseURL},
	} {
		mu.Lock()
		paths = nil
		mu.Unlock()
		in := ProviderInput{Name: "oc", Type: "opencode", Model: "glm-5.3", BaseURL: tc.apiBase, AuthPath: path, ProxyURL: proxy, RetryDisabled: true}
		p, err := NewProvider(in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil); err != nil {
			t.Fatalf("api_base %q: %v", tc.apiBase, err)
		}
		if _, err := ListModels(context.Background(), in); err != nil {
			t.Fatalf("api_base %q catalog: %v", tc.apiBase, err)
		}
		mu.Lock()
		got := append([]string(nil), paths...)
		mu.Unlock()
		if len(got) != 2 || got[0] != tc.chat || got[1] != tc.catalog {
			t.Errorf("api_base %q reached %v, want %s then %s", tc.apiBase, got, tc.chat, tc.catalog)
		}
		labelled := labelProvider(&labelTestProvider{err: errors.New("boom")}, in)
		if _, err := labelled.Complete(context.Background(), nil, nil); err == nil || !strings.Contains(err.Error(), "("+tc.label+")") {
			t.Errorf("api_base %q: label %v, want %s", tc.apiBase, err, tc.label)
		}
	}
}
