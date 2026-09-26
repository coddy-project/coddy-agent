package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// scopedRulesProject writes a root AGENTS.md plus one nested AGENTS.md per dir
// (slash-separated, relative to the project root) and returns a wired agent.
func scopedRulesProject(t *testing.T, dirs ...string) (*Agent, string) {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte("ROOT_AGENTS_TOKEN"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		full := filepath.Join(tmp, filepath.FromSlash(d))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "AGENTS.md"), []byte(nestedToken(d)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return rulesTestAgent(t, tmp), tmp
}

// rulesTestAgent wires an agent over a session in cwd with the rules of cwd
// discovered, as the manager does when it opens a session.
func rulesTestAgent(t testing.TB, cwd string) *Agent {
	t.Helper()
	cfg := &config.Config{Paths: config.Paths{CWD: cwd}}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	st := &session.State{ID: "t", CWD: cwd, Mode: session.ModeAgent}
	st.ReplaceRulesCatalog(session.DiscoverRules(cfg, cwd))
	return NewAgent(cfg, st, nil, nil)
}

// writeGlobRule puts a rule gated on pattern into the project's .coddy/rules.
func writeGlobRule(t testing.TB, cwd, name, pattern, token string) {
	t.Helper()
	dir := filepath.Join(cwd, ".coddy", "rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ndescription: " + name + "\nglobs: " + pattern + "\nalwaysApply: false\n---\n\n" + token + "\n"
	if err := os.WriteFile(filepath.Join(dir, name+".mdc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func nestedToken(dir string) string {
	return "NESTED_TOKEN_" + strings.ToUpper(strings.NewReplacer("/", "_", "-", "_").Replace(dir))
}

func readCall(id, path string) llm.ToolCall {
	return llm.ToolCall{ID: id, Name: "read", InputJSON: `{"path":"` + path + `"}`}
}

// recordResult adds the call and its result to the transcript, the way the
// loop does once the call ran.
func recordResult(a *Agent, tc llm.ToolCall, cwd string) llm.Message {
	a.state.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{tc}})
	msg := toolResultMessage(tc, "output of "+tc.ID, nil, a.toolCallRules("agent", tc, cwd))
	a.state.AddMessage(msg)
	return msg
}

func TestScopedAgentsRuleArrivesWithTheCallThatEntersItsDirectory(t *testing.T) {
	a, tmp := scopedRulesProject(t, "internal/agent", "external/httpserver")

	before := a.buildSystemPrompt("agent", nil, nil, nil)
	if !strings.Contains(before, "ROOT_AGENTS_TOKEN") {
		t.Fatal("root AGENTS.md must always be in the prompt")
	}

	got := a.toolCallRules("agent", readCall("r1", "internal/agent/react.go"), tmp)
	if !strings.Contains(got, nestedToken("internal/agent")) {
		t.Fatalf("the read did not bring the nested AGENTS.md of its folder: %q", got)
	}
	if strings.Contains(got, nestedToken("external/httpserver")) {
		t.Fatal("an untouched sibling directory's AGENTS.md came along")
	}
	if !strings.HasPrefix(got, toolCallRulesLead) || !strings.Contains(got, `kind="rule"`) {
		t.Fatalf("the rules do not open with the lead and a rule attachment: %q", got)
	}
	// The system prompt is what the provider has cached: an activation never
	// rewrites it, on this turn or on the next one.
	if after := a.buildSystemPrompt("agent", nil, nil, nil); after != before {
		t.Fatal("an activated rule changed the system prompt")
	}
}

func TestToolCallRulesAreDeliveredOnce(t *testing.T) {
	a, tmp := scopedRulesProject(t, "internal/agent")

	first := recordResult(a, readCall("r1", "internal/agent/react.go"), tmp)
	if !strings.Contains(first.Rules, nestedToken("internal/agent")) {
		t.Fatalf("the first read carries no rule: %+v", first)
	}
	if strings.Contains(first.Content, nestedToken("internal/agent")) {
		t.Fatal("the rule was written into the output a surface shows")
	}
	if again := a.toolCallRules("agent", readCall("r2", "internal/agent/loop.go"), tmp); again != "" {
		t.Fatalf("a rule the model can read in the history was attached again: %q", again)
	}
}

func TestToolCallRulesComeBackAfterACompaction(t *testing.T) {
	a, tmp := scopedRulesProject(t, "internal/agent")
	recordResult(a, readCall("r1", "internal/agent/react.go"), tmp)

	first := a.state.GetMessages()[1].Rules
	// A summarizer that copied the attachment into its summary has retold the
	// rule, not delivered it: the next match still attaches it whole.
	a.state.(*session.State).InsertCompactionSummary(len(a.state.GetMessages()), session.NewCompactionSummaryMessage("folded; "+first, "fake/model"))
	if got := a.toolCallRules("agent", readCall("r2", "internal/agent/react.go"), tmp); !strings.Contains(got, nestedToken("internal/agent")) {
		t.Fatalf("the rule folded away by the compaction did not come back: %q", got)
	}
}

func TestToolCallRulesSkipARuleAMentionDelivered(t *testing.T) {
	tmp := t.TempDir()
	writeGlobRule(t, tmp, "gofiles", "**/*.go", "MENTIONED_GO_RULE_TOKEN")
	for _, name := range []string{"main.go", "other.go"} {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := rulesTestAgent(t, tmp)

	blocks := a.attachActivatedRules([]acp.ContentBlock{
		{Type: acp.ContentTypeText, Text: "look at main.go"},
		{Type: acp.ContentTypeResource, Resource: &acp.Resource{URI: "file://" + filepath.ToSlash(filepath.Join(tmp, "main.go")), Text: "package main\n"}},
	})
	a.state.AddMessage(llm.Message{Role: llm.RoleUser, Content: contentBlocksToText(blocks)})
	if !strings.Contains(a.state.GetMessages()[0].Content, "MENTIONED_GO_RULE_TOKEN") {
		t.Fatal("the attached file did not bring the glob rule into its message")
	}
	if sys := a.buildSystemPrompt("agent", nil, nil, nil); strings.Contains(sys, "MENTIONED_GO_RULE_TOKEN") {
		t.Fatal("a glob rule reached the system prompt")
	}
	if got := a.toolCallRules("agent", readCall("r1", "other.go"), tmp); got != "" {
		t.Fatalf("a rule the user's message carries was attached to a tool result as well: %q", got)
	}
}

func TestAttachedFileRuleIsNotAttachedTwiceWhenTheUserNamedIt(t *testing.T) {
	tmp := t.TempDir()
	writeGlobRule(t, tmp, "gofiles", "**/*.go", "NAMED_GO_RULE_TOKEN")
	a := rulesTestAgent(t, tmp)
	rule := a.state.(*session.State).GetRulesCatalog()[0]

	blocks := a.attachActivatedRules([]acp.ContentBlock{
		{Type: acp.ContentTypeText, Text: "follow @rule:gofiles on main.go"},
		{Type: acp.ContentTypeResource, Resource: session.RuleAttachment(tmp, a.homeDir(), rule)},
		{Type: acp.ContentTypeResource, Resource: &acp.Resource{URI: "file://" + filepath.ToSlash(filepath.Join(tmp, "main.go"))}},
	})
	if n := strings.Count(contentBlocksToText(blocks), "NAMED_GO_RULE_TOKEN"); n != 1 {
		t.Fatalf("the rule is in the message %d times, want once", n)
	}
}

func TestScopedAgentsRuleNotActivatedByRunCommand(t *testing.T) {
	a, tmp := scopedRulesProject(t, "internal/agent")

	tc := llm.ToolCall{ID: "c1", Name: "run_command", InputJSON: `{"command":"go test ./internal/agent"}`}
	if got := a.toolCallRules("agent", tc, tmp); got != "" {
		t.Fatalf("run_command must not activate scoped rules: %q", got)
	}
}

func TestScopedAgentsRuleAncestorChain(t *testing.T) {
	a, tmp := scopedRulesProject(t, "a", "a/b", "a/b/c", "a/other")

	got := a.toolCallRules("agent", readCall("r1", "a/b/c/f.go"), tmp)
	for _, d := range []string{"a", "a/b", "a/b/c"} {
		if !strings.Contains(got, nestedToken(d)) {
			t.Fatalf("ancestor AGENTS.md for %q must come along", d)
		}
	}
	if strings.Contains(got, nestedToken("a/other")) {
		t.Fatal("a sibling of the touched directory must not come along")
	}
}

func TestScopedAgentsRuleMoveActivatesBothEnds(t *testing.T) {
	a, tmp := scopedRulesProject(t, "src", "dst")

	tc := llm.ToolCall{ID: "m1", Name: "mv", InputJSON: `{"src":"src/x.go","dst":"dst/x.go"}`}
	got := a.toolCallRules("agent", tc, tmp)
	for _, d := range []string{"src", "dst"} {
		if !strings.Contains(got, nestedToken(d)) {
			t.Fatalf("mv must bring the AGENTS.md of %q", d)
		}
	}
}

// A glob is written for files. A call that lists or searches a folder brings
// no glob rule; the read of a matching file inside it does.
func TestGlobRuleIgnoresADirectoryTheCallTargets(t *testing.T) {
	tmp := t.TempDir()
	writeGlobRule(t, tmp, "ui", "external/ui/**/*", "UI_RULE_TOKEN")
	if err := os.MkdirAll(filepath.Join(tmp, "external", "ui", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "external", "ui", "src", "App.tsx"), []byte("export {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := rulesTestAgent(t, tmp)

	for _, tc := range []llm.ToolCall{
		{ID: "g1", Name: "grep", InputJSON: `{"pattern":"App","path":"external/ui/src"}`},
		{ID: "g2", Name: "glob", InputJSON: `{"pattern":"*.tsx","path":"external/ui/src"}`},
	} {
		if got := a.toolCallRules("agent", tc, tmp); got != "" {
			t.Fatalf("%s over a folder brought a glob rule: %q", tc.Name, got)
		}
	}
	if got := a.toolCallRules("agent", readCall("r1", "external/ui/src/App.tsx"), tmp); !strings.Contains(got, "UI_RULE_TOKEN") {
		t.Fatalf("the read of a matching file brought no rule: %q", got)
	}
	// A file about to be written does not exist yet, and is still a file.
	write := llm.ToolCall{ID: "w1", Name: "write", InputJSON: `{"path":"external/ui/src/New.tsx","content":"x"}`}
	if got := a.toolCallRules("agent", write, tmp); !strings.Contains(got, "UI_RULE_TOKEN") {
		t.Fatalf("a write of a matching new file brought no rule: %q", got)
	}
}

// The rules a call brings are read before it runs, on the paths the model
// aimed it at: a write that creates a nested AGENTS.md does not hand the model
// back the text it has just written, while the folder's DESIGN.md that was
// already there comes along.
func TestAWriteDoesNotBringBackTheAgentsFileItCreates(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "pkg", "DESIGN.md"), []byte("PKG_DESIGN_TOKEN"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
		Tools:     config.Tools{PermissionMode: config.PermModeBypass},
		Paths:     config.Paths{CWD: tmp},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	st.ReplaceRulesCatalog(session.DiscoverRules(cfg, tmp))
	a := NewAgent(cfg, st, resumePermissionSender{}, nil)
	write := llm.ToolCall{ID: "w1", Name: "write", InputJSON: `{"path":"pkg/AGENTS.md","content":"WRITTEN_AGENTS_TOKEN"}`}
	provider := &pcScriptProvider{steps: []pcStep{{calls: []llm.ToolCall{write}}, {text: "done"}}}
	a.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }

	if _, err := a.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "document pkg"}}); err != nil {
		t.Fatal(err)
	}
	var result *llm.Message
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == "w1" {
			result = &m
		}
	}
	if result == nil {
		t.Fatal("the write left no result in the transcript")
	}
	if strings.Contains(result.Rules, "WRITTEN_AGENTS_TOKEN") {
		t.Fatalf("the write brought back the AGENTS.md it created: %q", result.Rules)
	}
	if !strings.Contains(result.Rules, "PKG_DESIGN_TOKEN") {
		t.Fatalf("the folder's DESIGN.md did not come with the write: %q", result.Rules)
	}
}

// The rules travel with a result to the model alone: the tool call update
// every surface shows, the result the bundle keeps for the tool call cards and
// the content of the transcript row are the tool's own output.
func TestSurfacesShowTheToolOutputWithoutItsRules(t *testing.T) {
	tmp := t.TempDir()
	writeGlobRule(t, tmp, "gofiles", "**/*.go", "SURFACE_RULE_TOKEN")
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionDir := t.TempDir()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
		Tools:     config.Tools{PermissionMode: config.PermModeBypass},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	st := &session.State{ID: "sess_surface", CWD: tmp, Mode: session.ModeAgent, SessionDir: sessionDir}
	st.ReplaceRulesCatalog(session.DiscoverRules(cfg, tmp))
	sender := &askModeSender{}
	a := NewAgent(cfg, st, sender, nil)
	provider := &pcScriptProvider{steps: []pcStep{{calls: []llm.ToolCall{readCall("r1", "main.go")}}, {text: "done"}}}
	a.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }

	if _, err := a.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "read main.go"}}); err != nil {
		t.Fatal(err)
	}
	if res, ok := toolResultOf(provider.seen[1], "r1"); !ok || !strings.Contains(res, "SURFACE_RULE_TOKEN") {
		t.Fatalf("the model was not sent the rule with the read: %q", res)
	}
	for _, u := range sender.updates {
		upd, ok := u.(acp.ToolCallStatusUpdate)
		if !ok {
			continue
		}
		for _, item := range upd.Content {
			if strings.Contains(item.Content.Text, "SURFACE_RULE_TOKEN") {
				t.Fatalf("a tool call update shows the rule: %q", item.Content.Text)
			}
		}
	}
	if saved, err := session.ReadToolCallResult(sessionDir, "r1"); err != nil || strings.Contains(saved, "SURFACE_RULE_TOKEN") || !strings.Contains(saved, "package main") {
		t.Fatalf("the bundle's result of the read = %q (%v), want the output alone", saved, err)
	}
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "SURFACE_RULE_TOKEN") {
			t.Fatalf("the transcript row's content carries the rule: %q", m.Content)
		}
	}
}

// A template with no {{.Rules}} in it asked for no rules at all, so none is
// attached to a tool result either.
func TestTemplateWithoutRulesGetsNoRulesWithToolResults(t *testing.T) {
	tmp := t.TempDir()
	writeGlobRule(t, tmp, "gofiles", "**/*.go", "NO_RULES_TEMPLATE_TOKEN")
	promptsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(promptsDir, "agent.md"), []byte("You are Coddy. {{.CWD}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := rulesTestAgent(t, tmp)
	a.cfg.Prompts.Dir = promptsDir

	if got := a.toolCallRules("agent", readCall("r1", "main.go"), tmp); got != "" {
		t.Fatalf("a template without {{.Rules}} still received a rule: %q", got)
	}
}

// The rules ride apart from the output in the transcript and join it only in
// what the provider is sent, so a result the eviction collapses keeps them.
func TestToolRulesJoinTheResultOnlyInWhatIsSent(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{readCall("r1", "main.go")}},
		{Role: llm.RoleTool, ToolCallID: "r1", Content: "file body\n", Rules: "RULES_TEXT"},
	}
	sent := withToolRules(msgs)
	if sent[2].Content != "file body\n\nRULES_TEXT" || sent[2].Rules != "" {
		t.Fatalf("sent result = %q (rules %q)", sent[2].Content, sent[2].Rules)
	}
	if msgs[2].Content != "file body\n" || msgs[2].Rules != "RULES_TEXT" {
		t.Fatal("joining the rules for the provider rewrote the transcript row")
	}
	if again := withToolRules(msgs); again[2].Content != sent[2].Content {
		t.Fatal("two sends of the same history differ")
	}
	plain := msgs[:2]
	if got := withToolRules(plain); &got[0] != &plain[0] {
		t.Fatal("a history without rules must be sent as it is")
	}

	evicted := append([]llm.Message(nil), msgs...)
	evicted[2].Content = "[evicted]"
	if got := withToolRules(evicted)[2].Content; got != "[evicted]\n\nRULES_TEXT" {
		t.Fatalf("an evicted result lost its rules: %q", got)
	}
}

// The standing part of the system prompt is rendered once per rules
// generation: an AGENTS.md edited mid-session does not move the system
// message, and the next generation reads it again.
func TestStandingPromptKeepsItsGenerationUntilTheCatalogIsReplaced(t *testing.T) {
	tmp := t.TempDir()
	agentsMD := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(agentsMD, []byte("AGENTS_V1"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := rulesTestAgent(t, tmp)
	first := a.buildSystemPrompt("agent", nil, nil, nil)
	if !strings.Contains(first, "AGENTS_V1") {
		t.Fatal("the first prompt does not carry AGENTS.md")
	}

	if err := os.WriteFile(agentsMD, []byte("AGENTS_V2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if again := a.buildSystemPrompt("agent", nil, nil, nil); again != first {
		t.Fatal("an AGENTS.md edited mid-session moved the system prompt")
	}

	a.rereadRules()
	if next := a.buildSystemPrompt("agent", nil, nil, nil); !strings.Contains(next, "AGENTS_V2") || strings.Contains(next, "AGENTS_V1") {
		t.Fatal("a new rules generation did not read AGENTS.md again")
	}
}

// A compaction starts the next rules generation: the history the provider had
// cached is rewritten anyway, so the standing rules are read again.
func TestCompactionReadsTheStandingRulesAgain(t *testing.T) {
	st := seededCompactState(t, 3)
	agentsMD := filepath.Join(st.GetCWD(), "AGENTS.md")
	if err := os.WriteFile(agentsMD, []byte("BEFORE_COMPACTION"), 0o644); err != nil {
		t.Fatal(err)
	}
	keep := 1
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, &compactCannedProvider{t: t, summary: "summary"})
	ag.cfg.Prompts.ApplyDefaults()
	if first := ag.buildSystemPrompt("agent", nil, nil, nil); !strings.Contains(first, "BEFORE_COMPACTION") {
		t.Fatal("the first prompt does not carry AGENTS.md")
	}
	if err := os.WriteFile(agentsMD, []byte("AFTER_COMPACTION"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.CompactSession(context.Background(), CompactOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if next := ag.buildSystemPrompt("agent", nil, nil, nil); !strings.Contains(next, "AFTER_COMPACTION") {
		t.Fatal("the compaction did not start a new rules generation")
	}
}

func TestExtractContextFilesStripsWindowsDriveSlash(t *testing.T) {
	blocks := []acp.ContentBlock{
		{Type: "resource", Resource: &acp.Resource{URI: "file:///C:/proj/x.go"}},
		{Type: "resource", Resource: &acp.Resource{URI: "file:///home/u/y.go"}},
		// A POSIX path that merely contains a colon is not a drive path.
		{Type: "resource", Resource: &acp.Resource{URI: "file:///a:b/z.go"}},
	}
	got := extractContextFiles(blocks)
	want := []string{"C:/proj/x.go", "/home/u/y.go", "/a:b/z.go"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
