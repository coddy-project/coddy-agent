package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// variantAgent builds an agent whose session runs modelRef on a provider row of
// the given name and type.
func variantAgent(t *testing.T, providerName, providerType, modelRef string) *Agent {
	t.Helper()
	st := &session.State{ID: "t", CWD: t.TempDir(), Mode: session.ModeAgent}
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: providerName, Type: providerType, APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: modelRef, MaxTokens: 100}},
		Agent:     config.Agent{Model: modelRef},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	return NewAgent(cfg, st, nil, nil)
}

func TestPromptVariantsOrderMostSpecificFirst(t *testing.T) {
	cases := []struct {
		name                              string
		providerName, providerType, model string
		want                              []string
	}{
		{"gemma 4 on neuraldeep", "neuraldeep", "neuraldeep", "neuraldeep/gemma-4-31b-noreason",
			[]string{"neuraldeep-gemma-4-31b-noreason", "gemma-4-31b-noreason", "gemma"}},
		{"gpt-oss on a local openai row", "local", "openai", "local/gpt-oss-20b",
			[]string{"local-gpt-oss-20b", "gpt-oss-20b", "gpt-oss"}},
		{"model without a family", "neuraldeep", "neuraldeep", "neuraldeep/kimi-k2.6",
			[]string{"neuraldeep-kimi-k2.6", "kimi-k2.6", "neuraldeep"}},
		{"unknown provider type", "custom", "openai-like", "custom/mistral-large",
			[]string{"custom-mistral-large", "mistral-large"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := variantAgent(t, tc.providerName, tc.providerType, tc.model).promptVariants(); !slices.Equal(got, tc.want) {
				t.Fatalf("promptVariants() = %v, want %v", got, tc.want)
			}
		})
	}
}

// A model the configuration cannot resolve (no models[] rows to fall back on)
// still contributes its own slug, and nothing else: no family is guessed from a
// name alone.
func TestPromptVariantsUnresolvedModel(t *testing.T) {
	a := variantAgent(t, "neuraldeep", "neuraldeep", "neuraldeep/gemma-4-31b")
	a.cfg.Models = nil
	a.cfg.Agent.Model = "elsewhere/gemma-4-31b"
	if got, want := a.promptVariants(), []string{"elsewhere-gemma-4-31b"}; !slices.Equal(got, want) {
		t.Fatalf("promptVariants() = %v, want %v", got, want)
	}
}

func TestBuildSystemPromptPerProviderSwitch(t *testing.T) {
	a := variantAgent(t, "neuraldeep", "neuraldeep", "neuraldeep/gemma-4-31b-noreason")

	on := a.buildSystemPrompt("agent", nil, nil, "", nil)
	if !strings.Contains(on, "Model-family notes (Gemma") {
		t.Fatal("a Gemma session should get the Gemma notes while model-tuned prompts are on")
	}

	off := false
	a.cfg.Prompts.PerProvider.Enabled = &off
	if got := a.buildSystemPrompt("agent", nil, nil, "", nil); strings.Contains(got, "Model-family notes") {
		t.Fatal("per_provider.enable=false must send the shared prompt")
	}
}

func TestBuildSystemPromptPerModelFileFromDir(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"agent.md":                            "SHARED {{.CWD}}",
		"agent.gemma.md":                      "FAMILY {{.CWD}}",
		"agent.neuraldeep-gemma-4-31b.md":     "PERMODEL {{.CWD}}",
		"plan.gemma.md":                       "FAMILY PLAN {{.CWD}}",
		"plan.md":                             "SHARED PLAN {{.CWD}}",
		"ask.md":                              "SHARED ASK {{.CWD}}",
		"agent.neuraldeep-qwen3.6-35b-a3b.md": "OTHER MODEL {{.CWD}}",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := variantAgent(t, "neuraldeep", "neuraldeep", "neuraldeep/gemma-4-31b")
	a.cfg.Prompts.Dir = dir

	for mode, want := range map[string]string{"agent": "PERMODEL", "plan": "FAMILY PLAN", "ask": "SHARED ASK"} {
		got := a.buildSystemPrompt(mode, nil, nil, "", nil)
		if !strings.Contains(got, want) {
			t.Errorf("%s: expected the %q file to be selected, got: %.120s", mode, want, got)
		}
		assertPromptIdentifiesCoddy(t, got, mode+" variant file")
	}
}

// The dedupe of the project AGENTS.md follows the template that is actually
// rendered: a variant file that drops {{.Rules}} must still carry the doc
// through {{.Instructions}}.
func TestBuildSystemPromptVariantWithoutRulesKeepsInstructions(t *testing.T) {
	a := variantAgent(t, "neuraldeep", "neuraldeep", "neuraldeep/gemma-4-31b")
	tmp := a.state.GetCWD()
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte("PROJECT_DOC_TOKEN"), 0o644); err != nil {
		t.Fatal(err)
	}
	promptsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(promptsDir, "agent.md"), []byte("You are Coddy.\n\n{{.Rules}}\n\n{{.Instructions}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "agent.gemma.md"), []byte("You are Coddy on Gemma.\n\n{{.Instructions}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := a.state.(*session.State)
	st.ReplaceRulesCatalog(session.DiscoverRules(&config.Config{}, tmp))
	a.cfg.Paths = config.Paths{CWD: tmp}
	a.cfg.Instructions.ApplyDefaults()
	a.cfg.Prompts.Dir = promptsDir

	prompt := a.buildSystemPrompt("agent", nil, nil, "", nil)
	if !strings.Contains(prompt, "on Gemma") {
		t.Fatalf("expected the gemma variant file, got:\n%s", prompt)
	}
	if n := strings.Count(prompt, "PROJECT_DOC_TOKEN"); n != 1 {
		t.Fatalf("the variant without {{.Rules}} carries the project AGENTS.md %d time(s), want 1:\n%s", n, prompt)
	}
}
