//go:build cli

package cli

// Edge cases of the console's provider picker (onboarding.go). The happy
// paths are features/cli_login.feature.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/providerpreset"
)

func preset(t *testing.T, id string) providerpreset.Preset {
	t.Helper()
	p, ok := providerpreset.ByID(id)
	if !ok {
		t.Fatalf("no preset %q", id)
	}
	return p
}

// paths of the staged commands, so a test says which parts of config.yaml a
// setup would write.
func paths(cmds []config.UCICommand) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, c.Op+" "+c.Path)
	}
	return out
}

func valueAt(cmds []config.UCICommand, path string) (string, bool) {
	for _, c := range cmds {
		if c.Path == path {
			return c.Value, true
		}
	}
	return "", false
}

// A row config.yaml already holds keeps everything the picker never asks
// about. A `set` on providers[name=X] replaces the whole list element, so a
// setup over an existing row must stage the scalars that moved and nothing
// else, or a credential helper, a proxy and a timeout go with the write.
func TestProviderSetupKeepsWhatAConfiguredRowAlreadyHolds(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{
			Name: "openai", Type: "openai",
			APIKeyCommand: "pass show openai", Proxy: "http://127.0.0.1:7890",
		}},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o"}},
	}
	cfg.Agent.Model = "openai/gpt-4o"

	// A typed key is the one change the operator made.
	run := &providerSetupRun{preset: preset(t, "openai"), apiKey: "sk-typed"}
	cmds, _ := providerSetupCommands(cfg, run, "openai/gpt-5.6-terra", nil)
	if slices.Contains(paths(cmds), "set providers[name=openai]") {
		t.Fatalf("the whole row was staged, which drops api_key_command and proxy: %v", paths(cmds))
	}
	if v, ok := valueAt(cmds, "providers[name=openai].api_key"); !ok || v != "sk-typed" {
		t.Fatalf("the typed key is not staged on its own path: %v", paths(cmds))
	}

	// An empty answer at the key prompt changes no credential at all.
	run = &providerSetupRun{preset: preset(t, "openai")}
	cmds, _ = providerSetupCommands(cfg, run, "openai/gpt-5.6-terra", nil)
	for _, p := range paths(cmds) {
		if strings.HasPrefix(p, "set providers[name=openai]") {
			t.Fatalf("an empty key answer touched the row: %v", paths(cmds))
		}
	}
}

// A row whose name the preset writes but whose type is another protocol
// would answer the endpoint just written in the wrong language.
func TestProviderSetupRepairsTheTypeOfAConfiguredRow(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{
		Name: "custom", Type: "anthropic", APIKey: "sk-old",
	}}}
	run := &providerSetupRun{preset: preset(t, "compatible"), apiBase: "https://api.example.test/v1"}
	cmds, _ := providerSetupCommands(cfg, run, "custom/mini-1", nil)
	if v, ok := valueAt(cmds, "providers[name=custom].type"); !ok || v != "openai" {
		t.Fatalf("the row kept a type the endpoint does not speak: %v", paths(cmds))
	}
}

// The listing of a configured row goes out the way that row's own requests
// do: the stored endpoint when the picker asked for none, and the stored
// credential when the operator answered the key prompt with an empty line,
// which is what the prompt promises. The credential stays behind when the
// endpoint moved, so a key of one server never reaches another.
func TestProviderListInputFillsInFromTheConfiguredRow(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cfg := &config.Config{Providers: []config.ProviderConfig{{
		Name: "openai", Type: "openai",
		APIBase: "https://gateway.example.test/v1", APIKey: "sk-saved",
		Proxy: "http://127.0.0.1:7890",
	}}}
	ctx := context.Background()

	in := providerListInput(ctx, cfg, &providerSetupRun{preset: preset(t, "openai")})
	if in.BaseURL != "https://gateway.example.test/v1" || in.APIKey != "sk-saved" {
		t.Fatalf("an empty answer did not reuse the row: base=%q key=%q", in.BaseURL, in.APIKey)
	}
	if in.ProxyURL != "http://127.0.0.1:7890" {
		t.Fatalf("the listing left the row's proxy: %q", in.ProxyURL)
	}

	// A typed key wins over the stored one.
	in = providerListInput(ctx, cfg, &providerSetupRun{preset: preset(t, "openai"), apiKey: "sk-typed"})
	if in.APIKey != "sk-typed" {
		t.Fatalf("the typed key did not win: %q", in.APIKey)
	}

	// Another endpoint means another server: the stored key stays behind.
	in = providerListInput(ctx, cfg, &providerSetupRun{preset: preset(t, "openai"), apiBase: "https://other.example.test/v1"})
	if in.APIKey != "" {
		t.Fatalf("the key stored for one endpoint went to another: %q", in.APIKey)
	}

	// Nothing configured: only what was answered.
	in = providerListInput(ctx, &config.Config{}, &providerSetupRun{
		preset: preset(t, "compatible"), apiBase: "https://api.example.test/v1", apiKey: "sk-new",
	})
	if in.BaseURL != "https://api.example.test/v1" || in.APIKey != "sk-new" || in.Type != "openai" {
		t.Fatalf("a fresh setup was filled in from nothing: %+v", in)
	}
}

// A provider config.yaml does not list is written whole, with the endpoint
// and the key that were answered.
func TestProviderSetupWritesAWholeRowForANewProvider(t *testing.T) {
	cfg := &config.Config{}
	run := &providerSetupRun{preset: preset(t, "compatible"), apiBase: "https://api.example.test/v1", apiKey: "sk-new"}
	cmds, added := providerSetupCommands(cfg, run, "custom/mini-1", nil)
	row, ok := valueAt(cmds, "providers[name=custom]")
	if !ok {
		t.Fatalf("no provider row staged: %v", paths(cmds))
	}
	for _, want := range []string{`"name":"custom"`, `"type":"openai"`, `"api_base":"https://api.example.test/v1"`, `"api_key":"sk-new"`} {
		if !strings.Contains(row, want) {
			t.Errorf("the staged row %s lacks %s", row, want)
		}
	}
	if !slices.Contains(added, "provider custom") || !slices.Contains(added, "model custom/mini-1") {
		t.Fatalf("what was added is not reported: %v", added)
	}
}

// agent.model moves only when what it names cannot be run.
func TestProviderSetupMovesAgentModelOnlyWhenItCannotBeRun(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CUSTOM_API_KEY", "")
	ref := "custom/mini-1"
	cases := []struct {
		name  string
		cfg   func() *config.Config
		moves bool
	}{
		{name: "nothing set", moves: true, cfg: func() *config.Config { return &config.Config{} }},
		{
			name: "a provider that cannot answer", moves: true,
			cfg: func() *config.Config {
				c := &config.Config{
					Providers: []config.ProviderConfig{{Name: "openai", Type: "openai"}},
					Models:    []config.ModelEntry{{Model: "openai/gpt-5.6-terra"}},
				}
				c.Agent.Model = "openai/gpt-5.6-terra"
				return c
			},
		},
		{
			name: "a model config.yaml does not list", moves: true,
			cfg: func() *config.Config {
				c := &config.Config{
					Providers: []config.ProviderConfig{{Name: "openai", Type: "openai", APIKey: "sk-set"}},
					Models:    []config.ModelEntry{{Model: "openai/gpt-5.6-terra"}},
				}
				// A new session would silently run on models[0] instead.
				c.Agent.Model = "openai/gone"
				return c
			},
		},
		{
			name: "a listed model of a provider that answers", moves: false,
			cfg: func() *config.Config {
				c := &config.Config{
					Providers: []config.ProviderConfig{{Name: "openai", Type: "openai", APIKey: "sk-set"}},
					Models:    []config.ModelEntry{{Model: "openai/gpt-5.6-terra"}},
				}
				c.Agent.Model = "openai/gpt-5.6-terra"
				return c
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := &providerSetupRun{preset: preset(t, "compatible"), apiBase: "https://api.example.test/v1"}
			cmds, _ := providerSetupCommands(tc.cfg(), run, ref, nil)
			_, moved := valueAt(cmds, "agent.model")
			if moved != tc.moves {
				t.Fatalf("agent.model moved = %v, want %v: %v", moved, tc.moves, paths(cmds))
			}
		})
	}
}

// Whether a model reads images is the model's own property. The
// OpenAI-compatible preset is multimodal because most endpoints behind it
// are, but a model the embedded catalog knows as text-only is written as
// text-only, so the composer does not offer it attachments.
func TestProviderSetupTakesMultimodalFromTheCatalog(t *testing.T) {
	compatible := preset(t, "compatible")
	if !compatible.Multimodal {
		t.Fatal("this test is about a text-only model under a multimodal preset")
	}
	cfg := &config.Config{}
	run := &providerSetupRun{preset: compatible, apiBase: "https://api.example.test/v1"}
	cmds, _ := providerSetupCommands(cfg, run, "custom/deepseek-v4-pro", nil)
	entry, ok := valueAt(cmds, "models[model=custom/deepseek-v4-pro]")
	if !ok {
		t.Fatalf("no model row staged: %v", paths(cmds))
	}
	if strings.Contains(entry, "multimodal") {
		t.Fatalf("a text-only model of the catalog was written multimodal: %s", entry)
	}
	// A model nothing knows keeps the preset's answer, which is all a
	// private endpoint can offer.
	cmds, _ = providerSetupCommands(cfg, run, "custom/private-vl", nil)
	entry, _ = valueAt(cmds, "models[model=custom/private-vl]")
	if !strings.Contains(entry, `"multimodal":true`) {
		t.Fatalf("a model the catalog does not know lost the preset's answer: %s", entry)
	}
}

// An ACP agent is a program this machine runs: the row carries its command
// and its arguments, and neither a key nor an endpoint is written for it.
func TestProviderSetupWritesAnACPRowAsItsCommand(t *testing.T) {
	cfg := &config.Config{}
	run := &providerSetupRun{preset: preset(t, "acp"), command: "  claude-code-acp --stdio  "}
	cmds, added := providerSetupCommands(cfg, run, "acp/sonnet", nil)
	row, ok := valueAt(cmds, "providers[name=acp]")
	if !ok {
		t.Fatalf("no provider row staged: %v", paths(cmds))
	}
	for _, want := range []string{`"type":"acp"`, `"command":"claude-code-acp"`, `"args":["--stdio"]`} {
		if !strings.Contains(row, want) {
			t.Errorf("the staged row %s lacks %s", row, want)
		}
	}
	for _, unwanted := range []string{"api_key", "api_base"} {
		if strings.Contains(row, unwanted) {
			t.Errorf("the staged row %s carries %s, which an ACP agent has no use for", row, unwanted)
		}
	}
	if !slices.Contains(added, "provider acp") {
		t.Fatalf("what was added is not reported: %v", added)
	}

	// A configured agent that already runs that command is left alone.
	cfg = &config.Config{Providers: []config.ProviderConfig{{
		Name: "acp", Type: "acp",
		ACP: &config.ACPProviderConfig{Command: "claude-code-acp", Args: []string{"--stdio"}},
	}}}
	cmds, _ = providerSetupCommands(cfg, run, "acp/sonnet", nil)
	for _, p := range paths(cmds) {
		if strings.HasPrefix(p, "set providers[name=acp]") {
			t.Fatalf("an unchanged command was staged again: %v", paths(cmds))
		}
	}

	// A different command moves only the acp block.
	run = &providerSetupRun{preset: preset(t, "acp"), command: "gemini --experimental-acp"}
	cmds, _ = providerSetupCommands(cfg, run, "acp/sonnet", nil)
	if v, ok := valueAt(cmds, "providers[name=acp].acp"); !ok || !strings.Contains(v, `"command":"gemini"`) {
		t.Fatalf("the new command is not staged on its own path: %v", paths(cmds))
	}
}

// The probe of an ACP agent is the command it runs, before any row exists.
func TestProviderListInputCarriesTheACPCommand(t *testing.T) {
	in := providerListInput(context.Background(), &config.Config{},
		&providerSetupRun{preset: preset(t, "acp"), command: "claude-code-acp --stdio"})
	if in.Type != "acp" || in.ACP == nil || in.ACP.Command != "claude-code-acp" {
		t.Fatalf("the listing does not carry the command: %+v", in)
	}
	if len(in.ACP.Args) != 1 || in.ACP.Args[0] != "--stdio" {
		t.Fatalf("the arguments were lost: %+v", in.ACP)
	}
}

// The listing of a provider's models is the one step of the setup with no
// modal on screen. A permission or question modal that opens meanwhile keeps
// the screen: taking it would leave its asker waiting for an answer nobody
// can give.
func TestAModelListingNeverTakesTheScreenFromAnotherModal(t *testing.T) {
	a := newTestApp(t)
	run := &providerSetupRun{preset: preset(t, "compatible"), apiBase: "https://api.example.test/v1"}
	a.setup = run
	gate := tui.NewText("a permission prompt", 0, 0, nil)
	a.openModal(gate)
	a.offerProviderModels(run, []llm.ModelEntry{{ID: "mini-1"}}, nil)
	if a.modal != tui.Component(gate) {
		t.Fatalf("the model selector replaced the modal on screen: %T", a.modal)
	}
	if a.setup != nil {
		t.Fatal("the setup is over once it could not go on")
	}
	if text := transcriptText(a); !strings.Contains(text, "provider setup stopped") {
		t.Fatalf("nothing says the setup stopped: %q", text)
	}
}

// An answer that belongs to a setup the console has moved on from is
// dropped: no second selector, no second write.
func TestAStaleModelListingIsDropped(t *testing.T) {
	a := newTestApp(t)
	old := &providerSetupRun{preset: preset(t, "compatible"), apiBase: "https://api.example.test/v1"}
	a.setup = &providerSetupRun{preset: preset(t, "openai")}
	a.offerProviderModels(old, []llm.ModelEntry{{ID: "mini-1"}}, nil)
	if a.modal != nil {
		t.Fatalf("a stale listing opened a selector: %T", a.modal)
	}
}

// A sign-in is skipped only where it is one account for the whole machine
// with no endpoint to choose. A NeuralDeep or OpenCode credential belongs to
// the hub it was taken from, and the picker has just asked which hub to use,
// so a stored one never stands in for the endpoint in front of it.
func TestOnlyAMachineWideSignInSkipsTheSignInStep(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv(llm.EnvDevinCLICredentials, filepath.Join(home, "no-devin-cli.toml"))
	for _, name := range []string{"CODEX_API_KEY", "NEURALDEEP_API_KEY", "OPENCODE_API_KEY", "DEVIN_API_KEY"} {
		t.Setenv(name, "")
	}
	cfg := &config.Config{Paths: config.Paths{Home: home, CWD: home, ConfigPath: filepath.Join(home, "config.yaml")}}
	for _, id := range []string{"codex", "devin", "neuraldeep", "opencode"} {
		if presetAlreadySignedIn(cfg, preset(t, id)) {
			t.Fatalf("%s is signed in on a machine with no credential", id)
		}
	}

	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(config.CodexAuthPath(home, "codex"), `{"auth_mode":"chatgpt","tokens":{"access_token":"x","refresh_token":"y"}}`)
	write(config.NeuralDeepAuthPath(home, "neuraldeep"), `{"api_key":"nd-stored","hub":"https://api.neuraldeep.ru/v1"}`)
	write(config.OpenCodeAuthPath(home, "opencode"), `{"api_key":"oc-stored"}`)

	write(config.DevinAuthPath(home, "devin"), `{"session_token":"dv-session"}`)

	for _, id := range []string{"codex", "devin"} {
		if !presetAlreadySignedIn(cfg, preset(t, id)) {
			t.Fatalf("a stored %s sign-in skips the sign-in step", id)
		}
	}
	// A key is not a sign-in: with one in the environment and no credential
	// stored, the picker must still run the sign-in, because the listing of a
	// row config.yaml does not list yet carries no variable.
	bare := &config.Config{Paths: config.Paths{Home: t.TempDir(), CWD: home, ConfigPath: filepath.Join(home, "config.yaml")}}
	t.Setenv("DEVIN_API_KEY", "dv-from-the-environment")
	if presetAlreadySignedIn(bare, preset(t, "devin")) {
		t.Fatal("DEVIN_API_KEY is a credential, not a sign-in")
	}
	t.Setenv("DEVIN_API_KEY", "")
	for _, id := range []string{"neuraldeep", "opencode"} {
		if presetAlreadySignedIn(cfg, preset(t, id)) {
			t.Fatalf("%s has a hub to choose, so a stored credential must not skip its sign-in", id)
		}
	}

	// The credential a signed-in provider is listed with travels on the
	// listing input; without it the probe would reach the backend bare.
	in := providerListInput(context.Background(), cfg, &providerSetupRun{preset: preset(t, "codex")})
	if in.AuthPath != config.CodexAuthPath(home, "codex") {
		t.Fatalf("the listing does not name the stored credential: %+v", in)
	}
	if in.NoCLILogin {
		t.Fatal("the row a fresh install would write may use the Codex CLI's login")
	}
}

func TestKimiPresetExists(t *testing.T) {
	if _, ok := providerpreset.ByID("kimi"); !ok {
		t.Fatal("Kimi is absent from the picker")
	}
}

func TestKimiPlatformSetupCarriesRouteAndKeepsExistingModel(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi", Mode: "platform", Region: "china", Proxy: "http://proxy:8080"}}, Models: []config.ModelEntry{{Model: "kimi/kimi-for-coding", MaxTokens: 123}}}
	run := &providerSetupRun{preset: preset(t, "kimi"), kimiMode: "platform", kimiRegion: "china", apiKey: "test-key"}
	in := providerListInput(context.Background(), cfg, run)
	if in.Mode != "platform" || in.Region != "china" || in.ProxyURL != "http://proxy:8080" {
		t.Fatalf("wrong route: %+v", in)
	}
	cmds, _ := providerSetupCommands(cfg, run, "kimi/kimi-for-coding", nil)
	if _, ok := valueAt(cmds, "models[model=kimi/kimi-for-coding]"); ok {
		t.Fatal("existing model overwritten")
	}
	fresh, _ := providerSetupCommands(&config.Config{}, run, "kimi/kimi-for-coding", nil)
	row, _ := valueAt(fresh, "providers[name=kimi]")
	for _, want := range []string{`"mode":"platform"`, `"region":"china"`} {
		if !strings.Contains(row, want) {
			t.Errorf("row %s lacks %s", row, want)
		}
	}
}

func TestKimiProbeUsesManagedIdentity(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi", KimiIdentity: id, Mode: "subscription", Region: "china"}}}
	in := providerListInput(context.Background(), cfg, &providerSetupRun{preset: preset(t, "kimi"), kimiMode: "subscription", kimiRegion: "china"})
	if in.KimiIdentity != id || in.AuthPath != config.ProviderConfigAuthPath(cfg.Paths.Home, cfg.Providers[0]) {
		t.Fatalf("managed identity lost: %+v", in)
	}
}

func TestKimiPresetRecognizesGlobalCLILogin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	dir := filepath.Join(home, "credentials")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kimi-code-env-0e4f99c69cc27850.json"), []byte(`{"access_token":"synthetic"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}}
	if !presetAlreadySignedIn(cfg, preset(t, "kimi")) {
		t.Fatal("global CLI login was ignored")
	}
}

func TestKimiPresetCredentialSelection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	dir := filepath.Join(home, "credentials")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	china := filepath.Join(dir, "kimi-code.json")
	original := []byte(`{"access_token":"synthetic-china","refresh_token":"do-not-use"}`)
	if err := os.WriteFile(china, original, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}}
	p := preset(t, "kimi")
	for _, tc := range []struct {
		mode, region string
		want         bool
	}{
		{"subscription", "global", false}, {"subscription", "china", true}, {"platform", "china", false},
	} {
		if got := presetAlreadySignedIn(cfg, p, tc.mode, tc.region); got != tc.want {
			t.Errorf("%s/%s = %v, want %v", tc.mode, tc.region, got, tc.want)
		}
	}
	p.ProviderName = "other"
	cfg.Providers = []config.ProviderConfig{{Name: "kimi", Type: "kimi"}, {Name: "other", Type: "kimi"}}
	if presetAlreadySignedIn(cfg, p, "subscription", "china") {
		t.Fatal("another row used the CLI credential")
	}
	after, err := os.ReadFile(china)
	if err != nil || string(after) != string(original) {
		t.Fatal("CLI credential changed")
	}
}

func TestKimiCatalogModelMetadata(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		mode, region, protocol string
		image                  *bool
	}{
		{"subscription", "global", "kimi", &yes}, {"subscription", "china", "anthropic", &no}, {"platform", "china", "openai_responses", &yes},
	} {
		t.Run(tc.mode+tc.region+tc.protocol, func(t *testing.T) {
			run := &providerSetupRun{preset: preset(t, "kimi"), kimiMode: tc.mode, kimiRegion: tc.region}
			listed := &llm.ModelEntry{ID: "selected", ContextWindow: 131072, KimiProtocol: tc.protocol, KimiImageInput: tc.image, KimiReasoningLevels: []string{"low", "high"}, KimiDefaultReasoning: "high"}
			cmds, _ := providerSetupCommands(&config.Config{}, run, "kimi/selected", listed)
			raw, ok := valueAt(cmds, "models[model=kimi/selected]")
			if !ok {
				t.Fatal("model was not staged")
			}
			var entry config.ModelEntry
			if err := yaml.Unmarshal([]byte(raw), &entry); err != nil {
				t.Fatal(err)
			}
			if entry.KimiMetadata == nil || entry.KimiMetadata.Model != "kimi/selected" || entry.KimiMetadata.Mode != tc.mode || entry.KimiMetadata.Region != tc.region || entry.KimiMetadata.Protocol != tc.protocol {
				t.Errorf("metadata: %+v", entry.KimiMetadata)
			}
			if entry.MaxContextTokens != 131072 || entry.Multimodal != *tc.image || (entry.ReasoningLevels == nil || !slices.Equal(*entry.ReasoningLevels, []string{"low", "high"})) || entry.ReasoningDefault != "high" {
				t.Errorf("capabilities: %+v", entry)
			}
		})
	}
}

func TestKimiUnsupportedAndManualCatalogModel(t *testing.T) {
	run := &providerSetupRun{preset: preset(t, "kimi"), kimiMode: "platform", kimiRegion: "global"}
	bad := &llm.ModelEntry{ID: "bad", KimiProtocol: "future-protocol"}
	cmds, _ := providerSetupCommands(&config.Config{}, run, "kimi/bad", bad)
	if _, ok := valueAt(cmds, "models[model=kimi/bad]"); ok {
		t.Fatal("unsupported protocol was saved")
	}
	if _, ok := valueAt(cmds, "agent.model"); ok {
		t.Fatal("unsupported protocol became default")
	}
	cmds, _ = providerSetupCommands(&config.Config{}, run, "kimi/manual", nil)
	raw, _ := valueAt(cmds, "models[model=kimi/manual]")
	var row map[string]any
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		t.Fatal(err)
	}
	if _, ok := row["kimi_metadata"]; ok {
		t.Fatal("manual model invented metadata")
	}
	cfg := &config.Config{Models: []config.ModelEntry{{Model: "kimi/selected", MaxContextTokens: 99}}}
	cmds, _ = providerSetupCommands(cfg, run, "kimi/selected", &llm.ModelEntry{ID: "selected", KimiProtocol: "kimi", ContextWindow: 200})
	if _, ok := valueAt(cmds, "models[model=kimi/selected]"); ok {
		t.Fatal("existing model was overwritten")
	}
}

func TestKimiCatalogCanonicalDefaultsAndUnknownCapabilities(t *testing.T) {
	run := &providerSetupRun{preset: preset(t, "kimi")}
	listed := &llm.ModelEntry{ID: "selected", KimiProtocol: "kimi"}
	cmds, _ := providerSetupCommands(&config.Config{}, run, "kimi/selected", listed)
	raw, ok := valueAt(cmds, "models[model=kimi/selected]")
	if !ok {
		t.Fatal("model was not staged")
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		t.Fatal(err)
	}
	md, ok := row["kimi_metadata"].(map[string]any)
	if !ok || md["mode"] != "subscription" || md["region"] != "global" {
		t.Fatalf("wrong defaults: %v", row)
	}
	for _, key := range []string{"multimodal", "reasoning_levels", "reasoning_default"} {
		if _, ok := row[key]; ok {
			t.Errorf("unknown %s was invented", key)
		}
	}
	run.kimiRegion = "invalid"
	cmds, _ = providerSetupCommands(&config.Config{}, run, "kimi/selected", listed)
	if len(cmds) != 0 {
		t.Fatalf("invalid region accepted: %v", cmds)
	}
}
