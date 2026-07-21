package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// --- Shared test doubles ---------------------------------------------------

type resumePermissionSender struct{}

func (resumePermissionSender) SendSessionUpdate(string, interface{}) error { return nil }

func (resumePermissionSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}

func (resumePermissionSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

type resumePermissionProvider struct {
	t    *testing.T
	seen []llm.Message
}

func (p *resumePermissionProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	p.t.Fatal("Complete must not be used by ResumeAfterPermission")
	return nil, nil
}

func (p *resumePermissionProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.seen = append([]llm.Message(nil), messages...)
	onChunk(llm.StreamChunk{TextDelta: "continued"})
	return &llm.Response{Content: "continued", StopReason: "end_turn"}, nil
}

// emptyThenAnswerProvider mimics a gpt-oss / harmony endpoint that ends its first turn
// with only internal reasoning (a tool call that leaked into the reasoning channel) and
// an empty content / empty tool_calls response, then answers normally when re-prompted.
// Without a continuation nudge the ReAct loop would dead-end the turn on a lone
// "thinking" bubble, leaving the user with no visible answer.
type emptyThenAnswerProvider struct {
	calls int
}

func (p *emptyThenAnswerProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, nil
}

func (p *emptyThenAnswerProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	if p.calls == 1 {
		// Reasoning only, with the tool call leaked into the analysis channel as text;
		// no final content and no structured tool_calls.
		onChunk(llm.StreamChunk{ReasoningDelta: `Let's search config for moonshot.{"path":"/cfg","pattern":"moonshot","max_results":20}`})
		return &llm.Response{Content: "", StopReason: "end_turn"}, nil
	}
	onChunk(llm.StreamChunk{TextDelta: "Here is the real answer."})
	return &llm.Response{Content: "Here is the real answer.", StopReason: "end_turn"}, nil
}

// --- react.go: content blocks, context files, tool kind, command, memory ---

func TestContentBlocksToText_textAndResource(t *testing.T) {
	blocks := []acp.ContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "resource", Resource: &acp.Resource{URI: "file:///a/b.go", Text: "pkg main"}},
	}
	got := contentBlocksToText(blocks)
	if !strings.Contains(got, `<coddy_attachment path="`) ||
		!strings.Contains(got, `name="b.go"`) ||
		!strings.Contains(got, "<![CDATA[") ||
		!strings.Contains(got, "pkg main") ||
		!strings.Contains(got, "]]>") {
		t.Fatalf("unexpected XML bundle: %s", got)
	}
}

func TestExtractContextFiles_fileURI(t *testing.T) {
	blocks := []acp.ContentBlock{
		{Type: "resource", Resource: &acp.Resource{URI: "file:///tmp/x.txt", Text: "x"}},
		{Type: "resource", Resource: &acp.Resource{URI: "https://example.com/z", Text: ""}},
	}
	got := extractContextFiles(blocks)
	if len(got) != 1 || got[0] != "/tmp/x.txt" {
		t.Fatalf("got %#v", got)
	}
}

func TestToolKind(t *testing.T) {
	cases := []struct {
		name, want string
	}{
		{"read", "read"},
		{"glob", "read"},
		{"grep", "read"},
		{"write", "write"},
		{"apply_patch", "write"},
		{"run_command", "run_command"},
		{"mkdir", "write"},
		{"mcp_server__tool", "other"},
	}
	for _, tc := range cases {
		if g := toolKind(tc.name); g != tc.want {
			t.Errorf("toolKind(%q) = %q, want %q", tc.name, g, tc.want)
		}
	}
}

func TestExtractCommand(t *testing.T) {
	if g := extractCommand(`{"command":"ls -la"}`); g != "ls -la" {
		t.Fatalf("got %q", g)
	}
	if g := extractCommand(`{`); g != "" {
		t.Fatalf("invalid json: got %q", g)
	}
}

func TestFormatMergedMemory(t *testing.T) {
	if g := formatMergedMemory("", "facts"); g != "facts" {
		t.Fatalf("got %q", g)
	}
	if g := formatMergedMemory("note", ""); g != "Session notes:\nnote" {
		t.Fatalf("got %q", g)
	}
	want := "facts\n\nSession notes:\nnote"
	if g := formatMergedMemory("note", "facts"); g != want {
		t.Fatalf("got %q want %q", g, want)
	}
}

// --- react.go: ReAct loop empty-turn recovery ------------------------------

func TestRunReActLoopRecoversFromEmptyAssistantTurn(t *testing.T) {
	st := &session.State{
		ID:         "sess_empty_turn",
		CWD:        t.TempDir(),
		Mode:       session.ModeAgent,
		SessionDir: t.TempDir(),
	}
	provider := &emptyThenAnswerProvider{}
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return provider, nil
	}

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "do the thing"}})
	if err != nil {
		t.Fatal(err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop reason = %q, want end_turn", stop)
	}
	if provider.calls < 2 {
		t.Fatalf("provider called %d times; expected the loop to re-prompt after an empty (reasoning-only) turn", provider.calls)
	}
	msgs := st.GetMessages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant || last.Content != "Here is the real answer." {
		t.Fatalf("conversation dead-ended on a thinking-only turn: last message = %+v", last)
	}
}

// --- resume_permission.go --------------------------------------------------

func TestResumeAfterPermissionRejectContinuesWithoutExecutingTool(t *testing.T) {
	sessionDir := t.TempDir()
	st := &session.State{
		ID:         "sess_resume_reject",
		CWD:        t.TempDir(),
		Mode:       session.ModeAgent,
		SessionDir: sessionDir,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "run blocked command then continue"},
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:        "call_blocked",
					Name:      "run_command",
					InputJSON: `{"command":"printf SHOULD_NOT_RUN"}`,
				}},
			},
		},
	}
	provider := &resumePermissionProvider{t: t}
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return provider, nil
	}

	stop, err := ag.ResumeAfterPermission(context.Background(), "call_blocked", &acp.PermissionResult{
		Outcome:  "cancelled",
		OptionID: "reject",
	})
	if err != nil {
		t.Fatal(err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop reason %q", stop)
	}
	var toolMsg *llm.Message
	for i := range st.GetMessages() {
		m := st.GetMessages()[i]
		if m.Role == llm.RoleTool && m.ToolCallID == "call_blocked" {
			toolMsg = &m
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("missing resumed tool result message")
		return
	}
	if strings.Contains(toolMsg.Content, "SHOULD_NOT_RUN") {
		t.Fatalf("rejected permission executed the tool: %q", toolMsg.Content)
	}
	if toolMsg.Content != "permission denied by user" {
		t.Fatalf("tool result %q", toolMsg.Content)
	}
	if len(provider.seen) == 0 {
		t.Fatal("provider was not called to continue after rejected permission")
	}
	last := provider.seen[len(provider.seen)-1]
	if last.Role != llm.RoleTool || last.ToolCallID != "call_blocked" || last.Content != "permission denied by user" {
		t.Fatalf("provider did not receive denied tool result as latest message: %+v", last)
	}
	if got := st.GetMessages()[len(st.GetMessages())-1]; got.Role != llm.RoleAssistant || got.Content != "continued" {
		t.Fatalf("missing continuation assistant message: %+v", got)
	}
}

// --- system_prompt.go: context breakdown -----------------------------------

func TestComputeContextBreakdownSystemPromptNonZero(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	st := &session.State{ID: "t", CWD: t.TempDir(), Mode: session.ModeAgent}
	a := NewAgent(cfg, st, nil, nil)
	toolsMD := "## Tools\n\ntool_a: does things"
	_ = toolsMD
	_ = a.buildSystemPrompt("agent", nil, []llm.ToolDefinition{{Name: "tool_a", Description: "does things"}}, "", nil)
	b := st.GetLastContextBreakdown()
	if b == nil {
		t.Fatal("expected breakdown")
		return
	}
	if b.SystemPrompt <= 0 {
		t.Fatalf("expected system prompt tokens > 0, got %+v", b)
	}
	if b.ToolDefinitions <= 0 {
		t.Fatalf("expected tool definition tokens > 0, got %+v", b)
	}
	// Sanity: system includes agent.md body text.
	if b.SystemPrompt < 100 {
		t.Fatalf("system prompt estimate too small: %d", b.SystemPrompt)
	}
}

func TestBuildSystemPromptIncludesRuntimeEnvironment(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	st := &session.State{ID: "t", CWD: t.TempDir(), Mode: session.ModeAgent}
	a := NewAgent(cfg, st, nil, nil)
	a.environment = platform.Environment{
		OS:    "windows",
		Arch:  "amd64",
		Shell: platform.Shell{Kind: platform.ShellPwsh, Path: "pwsh"},
	}

	prompt := a.buildSystemPrompt("agent", nil, nil, "", nil)
	for _, want := range []string{"<os>windows</os>", "<arch>amd64</arch>", "<shell>pwsh</shell>"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt does not contain %q", want)
		}
	}
}

func TestComputeContextBreakdownSubtractsParts(t *testing.T) {
	full := strings.Repeat("x", 400) + "\n\n" + strings.Repeat("y", 200)
	skillsText := strings.Repeat("s", 100)
	toolsText := strings.Repeat("t", 80)
	rules := strings.Repeat("r", 40)
	b := computeContextBreakdown(full, skillsText, toolsText, rules, nil, nil)
	if b.SystemPrompt <= 0 {
		t.Fatalf("system tokens: %d", b.SystemPrompt)
	}
	if b.Skills != session.EstimateTokens(skillsText) {
		t.Fatalf("skills: got %d", b.Skills)
	}
}

// --- system_prompt.go: rules block -----------------------------------------

func TestBuildSystemPromptIncludesRulesBlock(t *testing.T) {
	tmp := t.TempDir()
	rulePath := filepath.Join(tmp, ".coddy", "rules")
	if err := os.MkdirAll(rulePath, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nalwaysApply: true\nglobs: ['**/*.go']\n---\nRULE_GLOB_TOKEN:xyz\n"
	if err := os.WriteFile(filepath.Join(rulePath, "go.mdc"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	st.ReplaceRulesCatalog(session.DiscoverRules(&config.Config{}, tmp))
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	a := NewAgent(cfg, st, nil, nil)
	prompt := a.buildSystemPrompt("agent", nil, nil, "", []string{filepath.Join(tmp, "main.go")})
	if !strings.Contains(prompt, "RULE_GLOB_TOKEN") {
		t.Fatal("expected rule token in prompt")
	}
	if strings.Contains(prompt, "## Active Skills") {
		t.Fatal("rule token should be under Rules not Skills heading")
	}
}

func TestBuildSystemPromptMentionOnlyRule(t *testing.T) {
	tmp := t.TempDir()
	rulePath := filepath.Join(tmp, ".coddy", "rules")
	if err := os.MkdirAll(rulePath, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nalwaysApply: false\ndescription: mention only\n---\nRULE_MENTION_ONLY:secret\n"
	if err := os.WriteFile(filepath.Join(rulePath, "mention_demo.mdc"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	st.ReplaceRulesCatalog(session.DiscoverRules(&config.Config{}, tmp))
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	a := NewAgent(cfg, st, nil, nil)
	without := a.buildSystemPrompt("agent", nil, nil, "hello", nil)
	if strings.Contains(without, "RULE_MENTION_ONLY") {
		t.Fatal("mention-only rule must not appear without @mention")
	}
	with := a.buildSystemPrompt("agent", nil, nil, "please @mention_demo now", nil)
	if !strings.Contains(with, "RULE_MENTION_ONLY") {
		t.Fatal("expected mention-only rule body with @mention_demo")
	}
}

func TestBuildSystemPromptProjectDocsInRules(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte("AGENTS_DOC_TOKEN"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "DESIGN.md"), []byte("DESIGN_DOC_TOKEN"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	st.ReplaceRulesCatalog(session.DiscoverRules(&config.Config{}, tmp))
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	a := NewAgent(cfg, st, nil, nil)
	prompt := a.buildSystemPrompt("agent", nil, nil, "", nil)
	if !strings.Contains(prompt, "AGENTS_DOC_TOKEN") || !strings.Contains(prompt, "DESIGN_DOC_TOKEN") {
		t.Fatal("expected project docs in rules block")
	}
	agentsIdx := strings.Index(prompt, "AGENTS_DOC_TOKEN")
	designIdx := strings.Index(prompt, "DESIGN_DOC_TOKEN")
	if agentsIdx < 0 || designIdx < 0 || agentsIdx > designIdx {
		t.Fatal("expected AGENTS.md before DESIGN.md in prompt")
	}
}

// --- system_prompt.go: skills injection ------------------------------------

// TestAugmentUserMessageWithInvokedSkills_bodyInjected verifies that when a user
// explicitly invokes a slash command (/find-skills), its body is prepended to the
// user message sent to the LLM. The chat display (stored Content) remains unchanged.
//
// Regression: previously the body was never emitted because filteredInvoke compared
// against activeGlobCanon which always included no-glob skills, preventing injection
// in both the system-prompt ephemeral section and (by extension) the user message.
func TestAugmentUserMessageWithInvokedSkills_bodyInjected(t *testing.T) {
	const body = "UNIQUE_FIND_SKILLS_BODY_TOKEN"
	sk := &skills.Skill{
		Name:        "SKILL",
		FilePath:    filepath.Join("skills", "find-skills", "SKILL.md"),
		Description: "find skills",
		Content:     body,
	}

	userText := "/find-skills search pdf"
	result := augmentUserMessageWithInvokedSkills(userText, []*skills.Skill{sk})

	if !strings.Contains(result, body) {
		t.Fatalf("expected skill body %q to be prepended to user message; got:\n%s", body, result)
	}
	if !strings.Contains(result, userText) {
		t.Fatalf("expected original user text %q to be preserved in result; got:\n%s", userText, result)
	}
	// Skill body must come BEFORE the original user text.
	if strings.Index(result, body) > strings.Index(result, userText) {
		t.Fatalf("skill body should appear before user text in augmented message")
	}
}

// TestAugmentUserMessageWithInvokedSkills_noSkillMatch returns userText unchanged when
// the invoked name does not match any loaded skill.
func TestAugmentUserMessageWithInvokedSkills_noSkillMatch(t *testing.T) {
	sk := &skills.Skill{
		Name:     "SKILL",
		FilePath: filepath.Join("skills", "other", "SKILL.md"),
		Content:  "other body",
	}
	userText := "/find-skills pdf"
	result := augmentUserMessageWithInvokedSkills(userText, []*skills.Skill{sk})
	if result != userText {
		t.Fatalf("expected unchanged userText when no skill matches; got:\n%s", result)
	}
}

// TestAugmentUserMessageWithInvokedSkills_noSlashCommand returns userText unchanged when
// the message contains no slash command.
func TestAugmentUserMessageWithInvokedSkills_noSlashCommand(t *testing.T) {
	sk := &skills.Skill{
		Name:     "SKILL",
		FilePath: filepath.Join("skills", "find-skills", "SKILL.md"),
		Content:  "body",
	}
	userText := "поищи что-нибудь"
	result := augmentUserMessageWithInvokedSkills(userText, []*skills.Skill{sk})
	if result != userText {
		t.Fatalf("expected unchanged userText when no slash command; got:\n%s", result)
	}
}

// TestBuildSkillsPromptMarkdown_catalogSkillBodyNotInSystemPrompt verifies that slash
// command skill bodies are NOT injected into the system prompt; only the catalog listing
// appears there. Bodies travel via user message augmentation instead.
func TestBuildSkillsPromptMarkdown_catalogSkillBodyNotInSystemPrompt(t *testing.T) {
	const body = "UNIQUE_FIND_SKILLS_BODY_TOKEN"
	sk := &skills.Skill{
		Name:        "SKILL",
		FilePath:    filepath.Join("skills", "find-skills", "SKILL.md"),
		Description: "find skills",
		Content:     body,
	}

	allLoaded := []*skills.Skill{sk}
	active := skills.FilterForContext(allLoaded, nil)

	result := buildSkillsPromptMarkdown(allLoaded, active)

	if strings.Contains(result, body) {
		t.Fatalf("slash command skill body should NOT be in system prompt; got:\n%s", result)
	}
	if !strings.Contains(result, "find-skills") {
		t.Fatalf("skill name should appear in the slash catalog; got:\n%s", result)
	}
}

// TestBuildSkillsPromptMarkdown_noGlobNonCatalogBodyInSystemPrompt verifies that a
// skill with no globs that is NOT in the catalog has its body in the system prompt.
func TestBuildSkillsPromptMarkdown_noGlobNonCatalogBodyInSystemPrompt(t *testing.T) {
	const body = "NO_GLOB_NON_CATALOG_BODY"
	// This skill uses a path that doesn't become a slash command name in the catalog.
	sk := &skills.Skill{
		Name:     "my-always-rule",
		FilePath: filepath.Join("rules", "my-always-rule.md"),
		Content:  body,
	}

	allLoaded := []*skills.Skill{sk}
	active := skills.FilterForContext(allLoaded, nil)

	result := buildSkillsPromptMarkdown(allLoaded, active)

	if !strings.Contains(result, body) {
		t.Fatalf("always-apply non-catalog skill body should be in system prompt; got:\n%s", result)
	}
}

// --- toolsets.go -----------------------------------------------------------

func TestPlanToolSetFiltersToReadWebAndShell(t *testing.T) {
	r := tools.NewRegistry()
	set := ToolSetForMode("plan")
	filtered := FilterToolDefinitions(r.AllToolDefinitions(), set)
	got := make(map[string]bool)
	for _, d := range filtered {
		got[d.Name] = true
	}
	for _, want := range []string{"read", "glob", "grep", "websearch", "webfetch", "run_command", "question", "plan_write", "plan_list", "plan_read"} {
		if !got[want] {
			t.Errorf("plan toolset should include %q", want)
		}
	}
	for _, forbid := range []string{"write", "coddy_todo_plan_read"} {
		if got[forbid] {
			t.Errorf("plan toolset should not include %q", forbid)
		}
	}
}

func TestToolSetForAgentIsUnrestricted(t *testing.T) {
	set := ToolSetForMode("agent")
	if !set.Unrestricted() {
		t.Fatal("agent mode should use unrestricted tool set")
	}
}

// --- compact.go: CompactSession ---------------------------------------------

// compactCannedProvider serves Complete (summarization) with a canned summary
// and fails the test if Stream is called.
type compactCannedProvider struct {
	t        *testing.T
	summary  string
	err      error
	requests [][]llm.Message
}

func (p *compactCannedProvider) Complete(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	p.requests = append(p.requests, append([]llm.Message(nil), messages...))
	if p.err != nil {
		return nil, p.err
	}
	return &llm.Response{Content: p.summary, StopReason: "end_turn"}, nil
}

func (p *compactCannedProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
	p.t.Fatal("Stream must not be called by CompactSession")
	return nil, nil
}

func compactTestAgent(t *testing.T, st *session.State, comp config.Compaction, provider llm.Provider) *Agent {
	t.Helper()
	ag := NewAgent(&config.Config{
		Providers:  []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:     []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:      config.Agent{Model: "fake/model"},
		Compaction: comp,
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return provider, nil
	}
	return ag
}

func seededCompactState(t *testing.T, exchanges int) *session.State {
	t.Helper()
	st := &session.State{
		ID:   "sess_compact_unit",
		CWD:  t.TempDir(),
		Mode: session.ModeAgent,
	}
	for i := 1; i <= exchanges; i++ {
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("question %d", i)})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("answer %d", i)})
	}
	return st
}

func TestCompactSessionInsertsSummaryAtBoundary(t *testing.T) {
	st := seededCompactState(t, 3)
	keep := 1
	provider := &compactCannedProvider{t: t, summary: "dense summary"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)

	res, err := ag.CompactSession(context.Background(), "focus on file paths")
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "dense summary" {
		t.Fatalf("summary = %q", res.Summary)
	}
	if res.CompactedMessages != 4 || res.KeptMessages != 2 {
		t.Fatalf("counts = %d/%d, want 4/2", res.CompactedMessages, res.KeptMessages)
	}

	msgs := st.GetMessages()
	if len(msgs) != 7 {
		t.Fatalf("len = %d, want 7", len(msgs))
	}
	if !msgs[4].CompactionSummary {
		t.Fatalf("summary not inserted before the kept tail: %+v", msgs[4])
	}

	// The summarization request must carry the head transcript and the extra instructions.
	if len(provider.requests) != 1 {
		t.Fatalf("Complete called %d times", len(provider.requests))
	}
	req := transcriptText(provider.requests[0])
	for _, want := range []string{"question 1", "answer 2", "focus on file paths"} {
		if !strings.Contains(req, want) {
			t.Fatalf("summarization request misses %q:\n%s", want, req)
		}
	}
	if strings.Contains(req, "question 3") {
		t.Fatalf("kept tail leaked into the summarization request:\n%s", req)
	}
}

func TestCompactSessionNothingToCompact(t *testing.T) {
	st := seededCompactState(t, 2)
	keep := 2
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, &compactCannedProvider{t: t, summary: "s"})

	if _, err := ag.CompactSession(context.Background(), ""); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("err = %v, want ErrNothingToCompact", err)
	}
	if len(st.GetMessages()) != 4 {
		t.Fatal("history must stay untouched")
	}
}

func TestCompactSessionDisabled(t *testing.T) {
	st := seededCompactState(t, 3)
	off := false
	ag := compactTestAgent(t, st, config.Compaction{Enabled: &off}, &compactCannedProvider{t: t, summary: "s"})

	if _, err := ag.CompactSession(context.Background(), ""); err == nil {
		t.Fatal("disabled compaction must error")
	}
}

func TestCompactSessionEmptySummaryFails(t *testing.T) {
	st := seededCompactState(t, 3)
	keep := 1
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, &compactCannedProvider{t: t, summary: "   "})

	if _, err := ag.CompactSession(context.Background(), ""); err == nil {
		t.Fatal("empty summary must error")
	}
	if len(st.GetMessages()) != 6 {
		t.Fatal("failed compaction must not mutate history")
	}
}

func TestCompactSessionProviderErrorKeepsHistory(t *testing.T) {
	st := seededCompactState(t, 3)
	keep := 1
	provider := &compactCannedProvider{t: t, err: errors.New("boom")}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)

	if _, err := ag.CompactSession(context.Background(), ""); err == nil {
		t.Fatal("provider error must propagate")
	}
	if len(st.GetMessages()) != 6 {
		t.Fatal("failed compaction must not mutate history")
	}
}

// --- compact.go: /compact command interception -------------------------------

func TestParseCompactCommand(t *testing.T) {
	cases := []struct {
		in      string
		wantOK  bool
		wantArg string
	}{
		{in: "/compact", wantOK: true},
		{in: "  /compact  ", wantOK: true},
		{in: "/compact focus on file paths", wantOK: true, wantArg: "focus on file paths"},
		{in: "/compact\nkeep decisions", wantOK: true, wantArg: "keep decisions"},
		{in: "/compacted", wantOK: false},
		{in: "hello /compact", wantOK: false},
		{in: "", wantOK: false},
	}
	for _, tc := range cases {
		arg, ok := parseCompactCommand(tc.in)
		if ok != tc.wantOK || arg != tc.wantArg {
			t.Errorf("parseCompactCommand(%q) = (%q, %v), want (%q, %v)", tc.in, arg, ok, tc.wantArg, tc.wantOK)
		}
	}
}

func TestRunCompactCommandDoesNotPersistUserMessage(t *testing.T) {
	st := seededCompactState(t, 3)
	keep := 1
	provider := &compactCannedProvider{t: t, summary: "dense summary"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "/compact focus on tests"}})
	if err != nil {
		t.Fatal(err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop = %q", stop)
	}

	msgs := st.GetMessages()
	for _, m := range msgs {
		if strings.Contains(m.Content, "/compact") {
			t.Fatalf("the /compact command leaked into history: %+v", m)
		}
	}
	if !msgs[4].CompactionSummary {
		t.Fatalf("summary not inserted at boundary: %+v", msgs[4])
	}
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant || !strings.Contains(strings.ToLower(last.Content), "compacted") {
		t.Fatalf("missing confirmation message: %+v", last)
	}
	if len(provider.requests) != 1 || !strings.Contains(transcriptText(provider.requests[0]), "focus on tests") {
		t.Fatalf("instructions not forwarded to the summarizer: %v", provider.requests)
	}
}

func TestRunCompactCommandNothingToCompact(t *testing.T) {
	st := seededCompactState(t, 1)
	keep := 2
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, &compactCannedProvider{t: t, summary: "s"})

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "/compact"}})
	if err != nil {
		t.Fatal(err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop = %q", stop)
	}
	msgs := st.GetMessages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant || !strings.Contains(last.Content, "Nothing to compact") {
		t.Fatalf("expected friendly notice, got %+v", last)
	}
	for _, m := range msgs {
		if m.CompactionSummary {
			t.Fatal("no summary row expected")
		}
	}
}

func TestRunCompactCommandDisabled(t *testing.T) {
	st := seededCompactState(t, 3)
	off := false
	ag := compactTestAgent(t, st, config.Compaction{Enabled: &off}, &compactCannedProvider{t: t, summary: "s"})

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "/compact"}})
	if err != nil {
		t.Fatal(err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop = %q", stop)
	}
	msgs := st.GetMessages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant || !strings.Contains(strings.ToLower(last.Content), "disabled") {
		t.Fatalf("expected disabled notice, got %+v", last)
	}
}

// --- compact.go: auto-compaction at the context threshold --------------------

func TestMaybeAutoCompactThresholdBoundary(t *testing.T) {
	cases := []struct {
		name        string
		est         int
		maxContext  int
		enabled     bool
		wantCompact bool
	}{
		{name: "exactly at threshold", est: 80, maxContext: 100, enabled: true, wantCompact: true},
		{name: "below threshold", est: 79, maxContext: 100, enabled: true, wantCompact: false},
		{name: "above threshold", est: 95, maxContext: 100, enabled: true, wantCompact: true},
		{name: "no context window", est: 1000, maxContext: 0, enabled: true, wantCompact: false},
		{name: "disabled", est: 1000, maxContext: 100, enabled: false, wantCompact: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := seededCompactState(t, 3)
			keep := 1
			comp := config.Compaction{KeepRecentTurns: &keep}
			if !tc.enabled {
				off := false
				comp.Enabled = &off
			}
			provider := &compactCannedProvider{t: t, summary: "auto summary"}
			ag := compactTestAgent(t, st, comp, provider)
			ag.cfg.Models[0].MaxContextTokens = tc.maxContext
			st.SetLastContextBreakdown(&session.ContextBreakdown{EstimatedTotal: tc.est})

			got := ag.maybeAutoCompact(context.Background())
			if got != tc.wantCompact {
				t.Fatalf("maybeAutoCompact = %v, want %v", got, tc.wantCompact)
			}
			hasSummary := false
			for _, m := range st.GetMessages() {
				if m.CompactionSummary {
					hasSummary = true
				}
			}
			if hasSummary != tc.wantCompact {
				t.Fatalf("summary row present = %v, want %v", hasSummary, tc.wantCompact)
			}
		})
	}
}

func TestMaybeAutoCompactFailOpen(t *testing.T) {
	st := seededCompactState(t, 3)
	keep := 1
	provider := &compactCannedProvider{t: t, err: errors.New("summarizer down")}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)
	ag.cfg.Models[0].MaxContextTokens = 100
	st.SetLastContextBreakdown(&session.ContextBreakdown{EstimatedTotal: 90})

	if ag.maybeAutoCompact(context.Background()) {
		t.Fatal("failed compaction must report false")
	}
	if len(st.GetMessages()) != 6 {
		t.Fatal("history must stay untouched on failure")
	}
}

// autoCompactRunProvider serves Complete (summary) and Stream (answer),
// recording stream requests so the test can assert the post-compaction window.
type autoCompactRunProvider struct {
	streamSeen [][]llm.Message
}

func (p *autoCompactRunProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return &llm.Response{Content: "auto summary", StopReason: "end_turn"}, nil
}

func (p *autoCompactRunProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.streamSeen = append(p.streamSeen, append([]llm.Message(nil), messages...))
	onChunk(llm.StreamChunk{TextDelta: "post-auto answer"})
	return &llm.Response{Content: "post-auto answer", StopReason: "end_turn"}, nil
}

func TestRunAutoCompactsBeforeFirstLLMCall(t *testing.T) {
	st := seededCompactState(t, 3)
	provider := &autoCompactRunProvider{}
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		// Tiny window: the system prompt alone exceeds 80% of 50 tokens.
		Models: []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 50}},
		Agent:  config.Agent{Model: "fake/model"},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return provider, nil
	}

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "continue please"}})
	if err != nil {
		t.Fatal(err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop = %q", stop)
	}

	hasSummary := false
	for _, m := range st.GetMessages() {
		if m.CompactionSummary {
			hasSummary = true
		}
	}
	if !hasSummary {
		t.Fatal("auto-compaction did not insert a summary row")
	}
	if len(provider.streamSeen) == 0 {
		t.Fatal("no stream request recorded")
	}
	first := provider.streamSeen[0]
	sawSummary := false
	for _, m := range first {
		if m.Role == llm.RoleSystem {
			continue
		}
		if m.CompactionSummary {
			sawSummary = true
			break
		}
		// Any non-summary history message before the summary means the window
		// was not rebuilt after compaction.
		t.Fatalf("first LLM request does not start from the summary: %+v", m)
	}
	if !sawSummary {
		t.Fatal("summary missing from the first LLM request")
	}
}
