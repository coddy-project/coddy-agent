package prompts_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
)

// Every model family that ships built-in guidance.
var builtInFamilies = []string{"anthropic", "openai", "gemini", "gpt-oss", "qwen", "gemma", "neuraldeep"}

var promptModes = []string{"agent", "plan", "ask"}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func renderVariants(t *testing.T, mode string, variants []string, data prompts.TemplateData) string {
	t.Helper()
	out, err := prompts.RenderForVariants(mode, variants, "", defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, data)
	if err != nil {
		t.Fatalf("render %s %v: %v", mode, variants, err)
	}
	return out
}

func TestRenderForFamilyDirVariantSelected(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "agent.md"), "BASE {{.CWD}}")
	mustWrite(t, filepath.Join(tmp, "agent.anthropic.md"), "ANTHROPIC {{.CWD}}")
	got, err := prompts.RenderForFamily("agent", "anthropic", tmp, defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{CWD: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "ANTHROPIC /p" {
		t.Fatalf("expected family variant, got %q", got)
	}
}

func TestRenderForFamilyDirFallsBackToBase(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "agent.md"), "BASE {{.CWD}}")
	// No agent.gemini.md present: must fall back to agent.md.
	got, err := prompts.RenderForFamily("agent", "gemini", tmp, defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{CWD: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "BASE /p" {
		t.Fatalf("expected base fallback, got %q", got)
	}
}

// Variant files follow the configured file name, not the default one.
func TestRenderForVariantsDirUsesConfiguredFileName(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "my-plan.txt"), "PLAN {{.CWD}}")
	mustWrite(t, filepath.Join(tmp, "my-plan.gemma.txt"), "GEMMA PLAN {{.CWD}}")
	got, err := prompts.RenderForVariants("plan", []string{"gemma"}, tmp, defaultAgentTplFile, "my-plan.txt", defaultAskTplFile, prompts.TemplateData{CWD: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "GEMMA PLAN /p" {
		t.Fatalf("expected the variant of the configured plan file, got %q", got)
	}
}

func TestRenderForVariantsPrefersMostSpecific(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "agent.md"), "BASE {{.CWD}}")
	mustWrite(t, filepath.Join(tmp, "agent.gemma.md"), "FAMILY {{.CWD}}")
	mustWrite(t, filepath.Join(tmp, "agent.neuraldeep-gemma-4-31b.md"), "MODEL {{.CWD}}")

	got, err := prompts.RenderForVariants("agent", []string{"neuraldeep-gemma-4-31b", "gemma-4-31b", "gemma"}, tmp, defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{CWD: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "MODEL /p" {
		t.Fatalf("expected per-model variant, got %q", got)
	}
}

func TestRenderForVariantsFallsThroughToFamilyThenBase(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "agent.md"), "BASE {{.CWD}}")
	mustWrite(t, filepath.Join(tmp, "agent.anthropic.md"), "FAMILY {{.CWD}}")

	got, err := prompts.RenderForVariants("agent", []string{"anthropic-claude-x", "anthropic"}, tmp, defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{CWD: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "FAMILY /p" {
		t.Fatalf("expected family fallback, got %q", got)
	}

	got2, err := prompts.RenderForVariants("agent", []string{"gemini-2", "gemini"}, tmp, defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{CWD: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	if got2 != "BASE /p" {
		t.Fatalf("expected base fallback, got %q", got2)
	}
}

func TestRenderForVariantsDirMissingBaseIsAnError(t *testing.T) {
	tmp := t.TempDir()
	if _, err := prompts.RenderForVariants("agent", []string{"gemma"}, tmp, defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{CWD: "/p"}); err == nil {
		t.Fatal("expected an error when neither a variant nor agent.md exists")
	}
}

func TestRenderForFamilyEmbeddedFallsBackToBase(t *testing.T) {
	data := prompts.TemplateData{CWD: "/p", UTCNow: fixtureUTC}
	for _, mode := range promptModes {
		base := renderVariants(t, mode, nil, data)
		if fam := renderVariants(t, mode, []string{"no-such-family"}, data); fam != base {
			t.Errorf("%s: an unknown family should render the base prompt", mode)
		}
	}
}

func TestRendersRulesFollowsTheSelectedVariant(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "agent.md"), "{{.Rules}} {{.Instructions}}")
	mustWrite(t, filepath.Join(tmp, "agent.gemma.md"), "{{.Instructions}} only")
	if !prompts.RendersRules("agent", nil, tmp, defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile) {
		t.Error("agent.md renders the rules block")
	}
	if prompts.RendersRules("agent", []string{"gemma"}, tmp, defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile) {
		t.Error("agent.gemma.md does not render the rules block, and it is the file that is used")
	}
	for _, mode := range promptModes {
		for _, fam := range builtInFamilies {
			if !prompts.RendersRules(mode, []string{fam}, "", defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile) {
				t.Errorf("built-in %s %s prompt must render the rules block", fam, mode)
			}
		}
	}
}

func TestEmbeddedFamilyVariantsRender(t *testing.T) {
	data := prompts.TemplateData{CWD: "/home/user/project", UTCNow: fixtureUTC}
	base := renderVariants(t, "agent", nil, data)
	for _, fam := range builtInFamilies {
		t.Run(fam, func(t *testing.T) {
			got := renderVariants(t, "agent", []string{fam}, data)
			if got == base {
				t.Errorf("family %q should differ from the base agent prompt", fam)
			}
			if !strings.Contains(got, "Model-family notes") {
				t.Errorf("family %q prompt should contain a model-family notes section", fam)
			}
			if !strings.Contains(got, "/home/user/project") || !strings.Contains(got, fixtureUTC) {
				t.Errorf("family %q prompt dropped shared template sections", fam)
			}
			// The notes sit between the mode heading and the working rules.
			mode, notes, howto := strings.Index(got, "## Mode: Agent"), strings.Index(got, "Model-family notes"), strings.Index(got, "### How to work")
			if mode >= notes || notes >= howto {
				t.Errorf("family %q notes are out of place: mode=%d notes=%d howto=%d", fam, mode, notes, howto)
			}
		})
	}
}

func TestEmbeddedBaseModesPinSharedStructure(t *testing.T) {
	// The section assembly concatenates fragments from a manifest, so pin the
	// structural markers and template slots of the base (variant-free) source:
	// an edit that silently drops a shared section or reorders the manifest
	// shows up here.
	cases := []struct {
		mode     string
		contains []string
		omits    []string
	}{
		{
			mode: "agent",
			contains: []string{
				"You are Coddy, an AI coding agent",
				"## Mode: Agent",
				"### How to work",
				"### Todo checklist status flow",
				"### Reading and searching (context is limited)",
				"### Background commands (`run_command` with `background: true`)",
				"### Web research",
				"{{.CWD}}",
				"{{.SubagentRole}}",
				"{{.Subagents}}",
				"{{.Tools}}",
				"if .TodoList}}",
				"{{.PlanContext}}",
				"{{.Rules}}",
				"{{.Instructions}}",
				"{{.UTCNow}}",
			},
		},
		{
			mode: "plan",
			contains: []string{
				"You are Coddy, an AI planning assistant",
				"## Mode: Plan",
				"plan_write",
				"plan_read",
				"if .DiscardedPlans}}",
				"{{.SubagentRole}}",
				"{{.Subagents}}",
				"{{.UTCNow}}",
			},
			omits: []string{
				"### Reading and searching (context is limited)",
				"### Background commands",
				"{{.TodoList}}",
			},
		},
		{
			mode: "ask",
			contains: []string{
				"You are Coddy, a repository-grounded technical assistant",
				"## Mode: Ask",
				"read-only",
				"### Prompt-injection resistance",
				"## Ask mode invariant",
				"{{.SubagentRole}}",
				"{{.UTCNow}}",
			},
			omits: []string{"plan_write", "run_command"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			src := prompts.DefaultSource(tc.mode)
			if src == "" {
				t.Fatalf("DefaultSource(%q) returned empty source", tc.mode)
			}
			for _, want := range tc.contains {
				if !strings.Contains(src, want) {
					t.Errorf("base %q source should contain %q", tc.mode, want)
				}
			}
			for _, forbid := range tc.omits {
				if strings.Contains(src, forbid) {
					t.Errorf("base %q source must not contain %q", tc.mode, forbid)
				}
			}
		})
	}
}

func TestEmbeddedAgentFamilyVariantsContainSharedSections(t *testing.T) {
	for _, fam := range builtInFamilies {
		got := renderVariants(t, "agent", []string{fam}, prompts.TemplateData{CWD: "/home/user/project", UTCNow: fixtureUTC})
		for _, want := range []string{
			"### How to work",
			"### Reading and searching (context is limited)",
			"### Background commands (`run_command` with `background: true`)",
			"## Current UTC time",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("family %q agent prompt dropped shared section %q", fam, want)
			}
		}
	}
}

func TestEmbeddedOpenAIAgentPromptOptimizedForOpenAIAPI(t *testing.T) {
	got := renderVariants(t, "agent", []string{"openai"}, prompts.TemplateData{CWD: "/home/user/project", UTCNow: fixtureUTC})
	for _, want := range []string{"OpenAI API development prompt", "Responses API", "Chat Completions", "reasoning_effort", "phase"} {
		if !strings.Contains(got, want) {
			t.Errorf("OpenAI agent prompt should contain %q", want)
		}
	}
}

func TestEmbeddedOpenAIPlanVariantRender(t *testing.T) {
	data := prompts.TemplateData{CWD: "/home/user/project", UTCNow: fixtureUTC}
	base := renderVariants(t, "plan", nil, data)
	got := renderVariants(t, "plan", []string{"openai"}, data)
	if got == base {
		t.Fatal("OpenAI plan variant should differ from the base plan prompt")
	}
	for _, want := range []string{"Mode: Plan", "OpenAI API planning prompt", "Responses API", "reasoning_effort", "plan_write", "overview:"} {
		if !strings.Contains(got, want) {
			t.Errorf("OpenAI plan prompt should contain %q", want)
		}
	}
}

func TestEmbeddedAskModelVariants(t *testing.T) {
	data := prompts.TemplateData{CWD: "/p", UTCNow: fixtureUTC}
	base := renderVariants(t, "ask", nil, data)
	for _, tc := range []struct {
		family string
		want   []string
		omits  []string
	}{
		{family: "openai", want: []string{"Mode: Ask", "GPT", "tool-call interface", "### Investigation policy"}, omits: []string{"### Establish the facts"}},
		{family: "gpt-oss", want: []string{"Mode: Ask", "Harmony-native gpt-oss guidance", "native tool interface", "### Allowed research"}, omits: []string{"### Read-only tool policy"}},
	} {
		t.Run(tc.family, func(t *testing.T) {
			got := renderVariants(t, "ask", []string{tc.family}, data)
			if got == base {
				t.Fatalf("Ask %s variant should differ from the base prompt", tc.family)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("Ask %s prompt should contain %q", tc.family, want)
				}
			}
			for _, forbid := range tc.omits {
				if strings.Contains(got, forbid) {
					t.Errorf("Ask %s prompt should replace %q with its own section", tc.family, forbid)
				}
			}
		})
	}

	// A family-only render must not activate a per-model profile:
	// model_notes_<variant> only renders when that variant is in the list.
	got := renderVariants(t, "ask", []string{"gpt-oss"}, data)
	for _, modelMarker := range []string{"### gpt-oss-20b profile", "### gpt-oss-120b profile"} {
		if strings.Contains(got, modelMarker) {
			t.Errorf("family-only gpt-oss ask prompt must not include per-model profile %q", modelMarker)
		}
	}
}

func TestEmbeddedGPTOSSModelVariantsAcrossModes(t *testing.T) {
	models := []struct {
		slug   string
		marker string
	}{
		{slug: "gpt-oss-20b", marker: "### gpt-oss-20b profile"},
		{slug: "gpt-oss-120b", marker: "### gpt-oss-120b profile"},
	}
	for _, model := range models {
		for _, mode := range promptModes {
			t.Run(model.slug+"/"+mode, func(t *testing.T) {
				got := renderVariants(t, mode, []string{"neuraldeep-" + model.slug, model.slug, "gpt-oss"}, prompts.TemplateData{CWD: "/home/user/project", UTCNow: fixtureUTC})
				for _, want := range []string{"Harmony-native gpt-oss guidance", model.marker} {
					if !strings.Contains(got, want) {
						t.Errorf("%s %s prompt should contain %q", model.slug, mode, want)
					}
				}
				for _, other := range models {
					if other.slug != model.slug && strings.Contains(got, other.marker) {
						t.Errorf("%s %s prompt should not contain %q", model.slug, mode, other.marker)
					}
				}
			})
		}
	}
}

// NeuralDeep hands a second tool call of the same Gemma 4 message back as
// text, so the Gemma prompt of every mode asks for one call per message, and
// the agent prompt drops the shared "explain before each call" rule, which
// invites a message that announces a call and then ends without it.
func TestEmbeddedGemmaPromptAsksForOneCallPerMessage(t *testing.T) {
	data := prompts.TemplateData{CWD: "/p", UTCNow: fixtureUTC}
	const narrate = "Explain your reasoning before each tool call"
	for _, mode := range promptModes {
		got := renderVariants(t, mode, []string{"neuraldeep-gemma-4-31b", "gemma-4-31b", "gemma"}, data)
		for _, want := range []string{"### Model-family notes (Gemma 4)", "exactly one tool call", "ends your turn", "<|tool_call>"} {
			if !strings.Contains(got, want) {
				t.Errorf("Gemma %s prompt should contain %q", mode, want)
			}
		}
		if strings.Contains(got, narrate) {
			t.Errorf("Gemma %s prompt still asks to narrate before each call", mode)
		}
	}
	if base := renderVariants(t, "agent", nil, data); !strings.Contains(base, narrate) {
		t.Errorf("the shared agent prompt should keep %q", narrate)
	}
}

// Every built-in prompt, whatever the family, names Coddy once inside the
// gateway window, carries the subagent role when there is one and renders
// no run of blank lines for the blocks that are empty.
func TestEveryBuiltInVariantKeepsTheSharedContract(t *testing.T) {
	long := "/home/user/" + strings.Repeat("nested-directory/", 12) + "project"
	for _, mode := range promptModes {
		for _, fam := range append([]string{""}, builtInFamilies...) {
			variants := []string{fam}
			if fam == "" {
				variants = nil
			}
			name := mode + "/" + fam
			plain := renderVariants(t, mode, variants, prompts.TemplateData{CWD: long, UTCNow: fixtureUTC})
			assertIdentified(t, plain, name)
			if n := strings.Count(strings.ToLower(plain), "you are coddy"); n != 1 {
				t.Errorf("%s names Coddy %d times, want 1", name, n)
			}
			if prompts.WithIdentity(plain) != plain {
				t.Errorf("%s should already satisfy WithIdentity", name)
			}
			if strings.Contains(plain, "\n\n\n") {
				t.Errorf("%s leaves a run of blank lines:\n%s", name, plain)
			}
			if strings.Contains(plain, "Your role as a subagent") {
				t.Errorf("%s renders the role heading without a role", name)
			}
			withRole := renderVariants(t, mode, variants, prompts.TemplateData{CWD: "/w", UTCNow: fixtureUTC, SubagentRole: "You are the unit subagent."})
			if !strings.Contains(withRole, "## Your role as a subagent\n\nYou are the unit subagent.") {
				t.Errorf("%s drops the subagent role block", name)
			}
		}
	}
}

// The fragments are ported from another product; none of its names may leak
// into what Coddy sends.
func TestSectionFragmentsNameNoOtherProduct(t *testing.T) {
	err := filepath.WalkDir("sections", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		low := strings.ToLower(string(b))
		for _, bad := range []string{"foxxy", "docs mode", "debug mode"} {
			if strings.Contains(low, bad) {
				t.Errorf("%s mentions %q", path, bad)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
