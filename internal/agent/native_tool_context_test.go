package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

func TestNativeToolContextCapturedRequests(t *testing.T) {
	memorySource, err := os.ReadFile(filepath.FromSlash("../../external/memory/prompts/memory_agent.md"))
	if err != nil {
		t.Fatal(err)
	}
	goalSource, err := os.ReadFile("goal_verifier.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                      string
		mode                      session.Mode
		childSource, customSource string
		markdown                  bool
	}{
		{name: "agent", mode: session.ModeAgent}, {name: "plan", mode: session.ModePlan}, {name: "ask", mode: session.ModeAsk},
		{name: "memory template", mode: session.ModeAgent, childSource: string(memorySource)},
		{name: "goal verifier template", mode: session.ModeAgent, childSource: string(goalSource)},
		{name: "custom child opt in", mode: session.ModeAgent, childSource: "Custom child tools:\n{{.Tools}}", markdown: true},
		{name: "custom opt in", mode: session.ModeAgent, customSource: "Custom tools:\n{{.Tools}}", markdown: true},
		{name: "custom omitted", mode: session.ModeAgent, customSource: "Custom role without a catalog."},
		{name: "custom false condition", mode: session.ModeAgent, customSource: "Custom role. {{if .Memory}}{{.Tools}}{{end}}"},
		{name: "custom transformed", mode: session.ModeAgent, customSource: `Custom tools: {{printf "%q" .Tools}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var meta *session.SubagentMeta
			// Exercise the real builtin template sources through the common child
			// renderer, without starting memory or goal-verification lifecycles.
			if tc.childSource != "" {
				meta = &session.SubagentMeta{Name: "system-fixture", Kind: "system-fixture", Tools: []string{"read", "srv__inspect"}, PromptTemplate: tc.childSource}
			}
			ag, st, _ := modelToolsAgentMeta(t, tc.mode, "fake/full", meta, toolRow{Model: "fake/full"}, toolRow{Model: "fake/small", Tools: []string{"read"}})
			st.AddSessionMCPClient(mcp.NewStaticClient("srv", []mcp.ToolInfo{{Name: "inspect", Description: "Complete MCP description. Preserve every detail of the contract.", InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ids": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string", "enum": []string{"alpha", "beta"}}, "minItems": 1}}, "additionalProperties": false}}}))
			if tc.customSource != "" {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "agent.md"), []byte(tc.customSource), 0o600); err != nil {
					t.Fatal(err)
				}
				ag.cfg.Prompts.Dir = dir
			}
			defs := ag.currentToolDefinitions(string(tc.mode))
			before, err := json.Marshal(defs)
			if err != nil {
				t.Fatal(err)
			}
			provider := &bddModelToolsProvider{}
			ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
			if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "Answer directly."}}); err != nil {
				t.Fatal(err)
			}
			if len(provider.offered) != 1 || len(provider.seen[0]) == 0 {
				t.Fatal("expected one captured provider request")
			}
			actual, _ := json.Marshal(provider.offered[0])
			if string(actual) != string(before) {
				t.Fatal("native definitions changed")
			}
			if !offersTool(defs, "read") || (modeOffersMCP(string(tc.mode)) && !offersTool(defs, "srv__inspect")) {
				t.Fatal("fixture did not exercise builtin/MCP offer")
			}
			system := provider.seen[0][0].Content
			catalog := tools.FormatDefinitionsForPrompt(defs)
			if strings.Contains(system, catalog) != tc.markdown {
				t.Fatal("system catalog does not match bundled/custom opt-in policy")
			}
			build := ag.buildSystemPromptParts(string(tc.mode), nil, defs)
			if (build.ToolsMD != "") != tc.markdown {
				t.Fatal("prompt estimate includes a catalog that was not rendered")
			}
		})
	}
}

func TestNativeToolContextEstimateCountsFullOfferAndRenderedCatalog(t *testing.T) {
	defs := []llm.ToolDefinition{
		{Name: "read", Description: strings.Repeat("Full builtin contract. ", 20), InputSchema: map[string]interface{}{"type": "object"}},
		{Name: "srv__inspect", Description: strings.Repeat("Full MCP contract. ", 20), InputSchema: map[string]interface{}{"type": "object"}},
	}
	native := func(d llm.ToolDefinition) int {
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		return session.EstimateContextTokens(string(raw))
	}
	for _, catalog := range []string{"", tools.FormatDefinitionsForPrompt(defs)} {
		full := "System instructions.\n" + catalog
		b := computeContextBreakdown(full, "", catalog, "", nil, false, defs)
		if b.ToolDefinitions != native(defs[0])+session.EstimateContextTokens(catalog) || b.MCP != native(defs[1]) {
			t.Fatalf("native/context categories omit names or descriptions: %+v", b)
		}
		if b.EstimatedTotal != session.EstimateContextTokens(full)+native(defs[0])+native(defs[1]) {
			t.Fatal("catalog or native definitions counted twice in total")
		}
	}
}
