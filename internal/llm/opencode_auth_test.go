package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// TestOpenCodeGatewayForBindsAStoredLoginToItsGateway: a login stores its key
// against one of the two gateways, and when the row names no gateway itself
// that is where the request goes - not the default, which would send the key
// at the other gateway. A row that does name one keeps saying where its own
// requests go.
func TestOpenCodeGatewayForBindsAStoredLoginToItsGateway(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode-auth.json")
	if err := SaveOpenCodeAuth(path, "console-key", opencodeConsoleBaseURL); err != nil {
		t.Fatalf("SaveOpenCodeAuth: %v", err)
	}

	got := opencodeGatewayFor("", "", path)
	if got != opencodeConsoleBaseURL {
		t.Errorf("opencodeGatewayFor(no row) = %q, want %s", got, opencodeConsoleBaseURL)
	}
	// The same holds for a row whose api_base names no OpenCode gateway: the
	// value is discarded, and the login's gateway is what the key is for.
	got = opencodeGatewayFor("", "https://custom.example/v1", path)
	if got != opencodeConsoleBaseURL {
		t.Errorf("opencodeGatewayFor(unusable row) = %q, want %s", got, opencodeConsoleBaseURL)
	}
	// A row that names a gateway is the operator's choice for it, and an
	// explicit api_key is not the login's key and is not bound to it.
	got = opencodeGatewayFor("", opencodeGoBaseURL, path)
	if got != opencodeGoBaseURL {
		t.Errorf("opencodeGatewayFor(row at Go) = %q, want %s", got, opencodeGoBaseURL)
	}
	got = opencodeGatewayFor("explicit", opencodeGoBaseURL, path)
	if got != opencodeGoBaseURL {
		t.Errorf("opencodeGatewayFor(explicit) = %q, want %s", got, opencodeGoBaseURL)
	}
	// No login at all: the row resolves as it always did.
	got = opencodeGatewayFor("", opencodeConsoleBaseURL, filepath.Join(t.TempDir(), "absent.json"))
	if got != opencodeConsoleBaseURL {
		t.Errorf("opencodeGatewayFor(no login) = %q, want %s", got, opencodeConsoleBaseURL)
	}
}

// TestOpenCodeLogicalGatewayIgnoresTheStandOverride: the wire protocol a model
// is served over follows the gateway the credential is for, so the stand that
// redirects the traffic must not change which protocol is chosen.
func TestOpenCodeLogicalGatewayIgnoresTheStandOverride(t *testing.T) {
	t.Setenv(EnvOpenCodeBaseURL, "https://stand.invalid/inference/go/v1")
	if got := opencodeLogicalGateway("", opencodeConsoleBaseURL, ""); got != opencodeConsoleBaseURL {
		t.Errorf("opencodeLogicalGateway = %q, want %s", got, opencodeConsoleBaseURL)
	}
	if got := opencodeGatewayFor("", opencodeConsoleBaseURL, ""); got != "https://stand.invalid/inference/go/v1" {
		t.Errorf("opencodeGatewayFor = %q, want the stand", got)
	}
}

// TestOpenCodeGatewayForEnvOverrideWins: stands and tests redirect the process
// as a whole, which has to hold for a login's key as much as for an explicit
// one.
func TestOpenCodeGatewayForEnvOverrideWins(t *testing.T) {
	t.Setenv(EnvOpenCodeBaseURL, "https://stand.invalid/inference/go/v1")
	path := filepath.Join(t.TempDir(), "opencode-auth.json")
	if err := SaveOpenCodeAuth(path, "console-key", opencodeConsoleBaseURL); err != nil {
		t.Fatalf("SaveOpenCodeAuth: %v", err)
	}
	if got := opencodeGatewayFor("", "", path); got != "https://stand.invalid/inference/go/v1" {
		t.Errorf("opencodeGatewayFor = %q, want the stand", got)
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
