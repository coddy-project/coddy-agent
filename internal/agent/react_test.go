package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks/hooktest"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/permission"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/todo"
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

type recordingPermissionSender struct {
	requests []acp.PermissionRequestParams
}

type todoSnapshotSender struct {
	updates []interface{}
}

func (s *todoSnapshotSender) SendSessionUpdate(_ string, update interface{}) error {
	s.updates = append(s.updates, update)
	return nil
}

func (*todoSnapshotSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}

func (*todoSnapshotSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func (s *recordingPermissionSender) SendSessionUpdate(string, interface{}) error { return nil }

func (s *recordingPermissionSender) RequestPermission(_ context.Context, p acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.requests = append(s.requests, p)
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}

func (s *recordingPermissionSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
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

type configReloadProvider struct {
	calls int
	tools [][]llm.ToolDefinition
}

func (p *configReloadProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, nil
}

func (p *configReloadProvider) Stream(_ context.Context, _ []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	p.tools = append(p.tools, append([]llm.ToolDefinition(nil), defs...))
	switch p.calls {
	case 1:
		call := llm.ToolCall{ID: "cfg-1", Name: "config_set", InputJSON: `{"commands":["set skills.auto_discovery=false"]}`}
		onChunk(llm.StreamChunk{ToolCall: &call})
		return &llm.Response{ToolCalls: []llm.ToolCall{call}, StopReason: "tool_use"}, nil
	case 2:
		call := llm.ToolCall{ID: "cfg-2", Name: "config_commit", InputJSON: `{}`}
		onChunk(llm.StreamChunk{ToolCall: &call})
		return &llm.Response{ToolCalls: []llm.ToolCall{call}, StopReason: "tool_use"}, nil
	}
	onChunk(llm.StreamChunk{TextDelta: "Configuration reloaded."})
	return &llm.Response{Content: "Configuration reloaded.", StopReason: "end_turn"}, nil
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
		{"coddy_docs_search", "read"},
		{"coddy_docs_read", "read"},
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

func TestTodoItemUpdateSavesAndPublishesFinalPlanSnapshot(t *testing.T) {
	dir := t.TempDir()
	st := &session.State{
		ID:         "sess_todo_snapshot",
		CWD:        dir,
		Mode:       session.ModeAgent,
		SessionDir: dir,
	}
	st.SetPlan([]acp.PlanEntry{
		{Content: "Inspect existing cards", Status: "completed"},
		{Content: "Render structured preview", Status: "pending"},
	})
	sender := &todoSnapshotSender{}
	ag := NewAgent(&config.Config{}, st, sender, nil)

	_, err := ag.executeToolCall(
		context.Background(),
		llm.ToolCall{
			ID:        "todo-update-1",
			Name:      todo.ToolNameItemUpdate,
			InputJSON: `{"index":1,"status":"completed"}`,
		},
		ag.buildToolEnv(string(session.ModeAgent), dir),
		string(session.ModeAgent),
		st.ID,
		false,
	)
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}

	meta, err := session.ReadToolCallMeta(dir, "todo-update-1")
	if err != nil {
		t.Fatalf("ReadToolCallMeta: %v", err)
	}
	if len(meta.PlanSnapshot) != 2 || meta.PlanSnapshot[1].Status != "completed" {
		t.Fatalf("persisted PlanSnapshot = %+v", meta.PlanSnapshot)
	}

	st.SetPlan([]acp.PlanEntry{{Content: "Later plan", Status: "pending"}})
	persisted, err := session.ReadToolCallMeta(dir, "todo-update-1")
	if err != nil || persisted.PlanSnapshot[1].Content != "Render structured preview" {
		t.Fatalf("historical plan snapshot changed: meta=%+v err=%v", persisted, err)
	}

	var completed acp.ToolCallStatusUpdate
	for _, update := range sender.updates {
		candidate, ok := update.(acp.ToolCallStatusUpdate)
		if ok && candidate.Status == "completed" {
			completed = candidate
		}
	}
	coddy, _ := completed.Meta["coddy"].(map[string]interface{})
	sent, _ := coddy["todoPlan"].([]acp.PlanEntry)
	if len(sent) != 2 || sent[1].Status != "completed" {
		t.Fatalf("SSE todoPlan = %+v", sent)
	}
}

func TestMCPToolDefinitionsFilter(t *testing.T) {
	clients := []*mcp.Client{
		mcp.NewStaticClient("srv", []mcp.ToolInfo{{Name: "echo"}, {Name: "write"}}),
		mcp.NewStaticClient("other", []mcp.ToolInfo{{Name: "echo"}}),
	}
	defs := mcpToolDefinitions(clients, func(server, tool string) bool {
		return server != "srv" || tool != "write"
	})
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	want := []string{"srv__echo", "other__echo"}
	if len(names) != len(want) || names[0] != want[0] || names[1] != want[1] {
		t.Fatalf("defs = %v, want %v", names, want)
	}
}

func TestCallMCPToolDisabledGuard(t *testing.T) {
	st := &session.State{
		ID:   "sess_mcp_guard",
		CWD:  t.TempDir(),
		Mode: session.ModeAgent,
		MCPFilterFactory: func() func(server, tool string) bool {
			return func(server, tool string) bool { return false }
		},
	}
	st.AddSessionMCPClient(mcp.NewStaticClient("srv", []mcp.ToolInfo{{Name: "echo"}}))
	ag := NewAgent(&config.Config{}, st, resumePermissionSender{}, nil)
	if _, err := ag.callMCPTool(context.Background(), "srv", "echo", "{}"); err == nil {
		t.Fatal("disabled MCP tool must be rejected at dispatch")
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

// The {{.Memory}} slot holds the session notes and nothing of the memory
// subagent: its report is per-turn text and rides in the turn context.
func TestFormatSessionNotes(t *testing.T) {
	if g := formatSessionNotes(""); g != "" {
		t.Fatalf("got %q", g)
	}
	if g := formatSessionNotes("note"); g != "Session notes:\nnote" {
		t.Fatalf("got %q", g)
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

func TestConfigSetRefreshesToolDefinitionsWithinSameTurn(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("skills:\n  auto_discovery: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Providers = []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}}
	cfg.Models = []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}}
	cfg.Agent.Model = "fake/model"
	st := &session.State{ID: "sess_config_reload", CWD: dir, Mode: session.ModeAgent}
	provider := &configReloadProvider{}
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
	ag.SetConfigReloader(func(context.Context) ([]string, error) { return nil, nil })
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }

	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "disable skill discovery"}}); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 3 {
		t.Fatalf("provider calls = %d, want stage, commit, and final answer", provider.calls)
	}
	contains := func(defs []llm.ToolDefinition, name string) bool {
		for _, def := range defs {
			if def.Name == name {
				return true
			}
		}
		return false
	}
	if !contains(provider.tools[0], "load_skill") {
		t.Fatal("load_skill should be present before config_set")
	}
	if !contains(provider.tools[1], "load_skill") {
		t.Fatal("staging alone must not reload the runtime")
	}
	if contains(provider.tools[2], "load_skill") {
		t.Fatal("load_skill should be removed after same-turn config_commit reload")
	}
}

// Committing the agent's own config can start MCP processes and change the
// permission policy itself, so accept_edits must still prompt for
// config_commit (unlike project file writes), the prompt must show the staged
// commands, and only the explicit bypass mode may skip the dialog.
func TestConfigCommitPermissionPerMode(t *testing.T) {
	for _, tc := range []struct {
		mode        string
		wantPrompts int
	}{
		{mode: config.PermModeAcceptEdits, wantPrompts: 1},
		{mode: config.PermModeBypass, wantPrompts: 0},
	} {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(configPath, []byte("skills:\n  auto_discovery: true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Providers = []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}}
		cfg.Models = []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}}
		cfg.Agent.Model = "fake/model"
		cfg.Tools.PermissionMode = tc.mode
		st := &session.State{ID: "sess_cfg_perm_" + tc.mode, CWD: dir, Mode: session.ModeAgent}
		sender := &recordingPermissionSender{}
		provider := &configReloadProvider{}
		ag := NewAgent(cfg, st, sender, nil)
		ag.SetConfigReloader(func(context.Context) ([]string, error) { return nil, nil })
		ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }

		if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "disable skill discovery"}}); err != nil {
			t.Fatalf("mode %s: %v", tc.mode, err)
		}
		var commitPrompts []acp.PermissionRequestParams
		for _, req := range sender.requests {
			if strings.Contains(req.ToolCall.Title, "config_commit") {
				commitPrompts = append(commitPrompts, req)
			}
		}
		if len(commitPrompts) != tc.wantPrompts {
			t.Fatalf("mode %s: config_commit permission prompts = %d, want %d", tc.mode, len(commitPrompts), tc.wantPrompts)
		}
		if tc.wantPrompts > 0 {
			body := ""
			for _, item := range commitPrompts[0].ToolCall.Content {
				body += item.Content.Text
			}
			if !strings.Contains(body, "set skills.auto_discovery=false") {
				t.Fatalf("mode %s: permission prompt does not show the staged commands: %q", tc.mode, body)
			}
		}
	}
}

// neverAnswersProvider always ends a turn with only reasoning (a leaked tool call)
// and never produces content or a structured tool_call, even after the continuation
// nudges. The loop must then surface a notice instead of returning end_turn with no
// visible reply.
type neverAnswersProvider struct{ calls int }

func (p *neverAnswersProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, nil
}

func (p *neverAnswersProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	onChunk(llm.StreamChunk{ReasoningDelta: `We should call run_command.{"command":"ls -R ."}`})
	return &llm.Response{Content: "", StopReason: "end_turn"}, nil
}

func TestRunReActLoopSurfacesNoticeWhenModelNeverAnswers(t *testing.T) {
	st := &session.State{
		ID:         "sess_never_answers",
		CWD:        t.TempDir(),
		Mode:       session.ModeAgent,
		SessionDir: t.TempDir(),
	}
	provider := &neverAnswersProvider{}
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return provider, nil
	}

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "do the thing"}})
	if err == nil {
		t.Fatal("expected an error surfacing the no-reply turn so the UI shows a notice, got nil")
	}
	if !strings.Contains(err.Error(), "no reply") {
		t.Fatalf("error should explain the empty (reasoning-only) turn: %v", err)
	}
	if stop != string(acp.StopReasonRefused) {
		t.Fatalf("stop = %q, want refused", stop)
	}
	// It should have tried the continuation nudge a bounded number of times first.
	if provider.calls < maxEmptyAssistantContinuations+1 {
		t.Fatalf("provider called %d times; expected retries before surfacing the notice", provider.calls)
	}
}

// progressThenAnswerProvider alternates empty (reasoning-only) turns with real
// tool calls before finally answering, mimicking a slow multi-step task on a
// gpt-oss / harmony endpoint. The loop must NOT give up while the model keeps
// making progress (executing tools) between empty thoughts: the empty-turn
// counter is for CONSECUTIVE stalls, not cumulative empties across the turn.
type progressThenAnswerProvider struct{ calls int }

func (p *progressThenAnswerProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, nil
}

func (p *progressThenAnswerProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	switch p.calls {
	case 2:
		tc := llm.ToolCall{ID: "tc1", Name: "glob", InputJSON: `{"pattern":"*"}`}
		onChunk(llm.StreamChunk{ToolCall: &tc})
		return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}, nil
	case 5:
		onChunk(llm.StreamChunk{TextDelta: "done"})
		return &llm.Response{Content: "done", StopReason: "end_turn"}, nil
	default: // 1, 3, 4: empty, reasoning-only turns
		onChunk(llm.StreamChunk{ReasoningDelta: "still thinking"})
		return &llm.Response{Content: "", StopReason: "end_turn"}, nil
	}
}

func TestRunReActLoopResetsEmptyCounterOnToolProgress(t *testing.T) {
	st := &session.State{
		ID:         "sess_progress",
		CWD:        t.TempDir(),
		Mode:       session.ModeAgent,
		SessionDir: t.TempDir(),
	}
	provider := &progressThenAnswerProvider{}
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return provider, nil
	}

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "convert the file"}})
	if err != nil {
		t.Fatalf("loop gave up while the model was still making progress: %v", err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop = %q, want end_turn (the model eventually answered)", stop)
	}
	if provider.calls < 5 {
		t.Fatalf("provider called %d times; the loop gave up before the model answered", provider.calls)
	}
	msgs := st.GetMessages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant || !strings.Contains(last.Content, "done") {
		t.Fatalf("final answer not reached: %+v", last)
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
	last := lastHistoryMessage(provider.seen)
	if last.Role != llm.RoleTool || last.ToolCallID != "call_blocked" || last.Content != "permission denied by user" {
		t.Fatalf("provider did not receive denied tool result as latest message: %+v", last)
	}
	if got := st.GetMessages()[len(st.GetMessages())-1]; got.Role != llm.RoleAssistant || got.Content != "continued" {
		t.Fatalf("missing continuation assistant message: %+v", got)
	}
}

// lastHistoryMessage is the newest message of a request that is part of the
// replayed conversation: the turn context block trails it and belongs to no
// transcript (turn_context.go).
func lastHistoryMessage(msgs []llm.Message) llm.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		if strings.Contains(msgs[i].Content, turnContextOpenTag) {
			continue
		}
		return msgs[i]
	}
	return llm.Message{}
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
	_ = a.buildSystemPrompt("agent", nil, []llm.ToolDefinition{{Name: "tool_a", Description: "does things"}})
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

	prompt := a.buildSystemPrompt("agent", nil, nil)
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
	always := "---\nalwaysApply: true\n---\nRULE_ALWAYS_TOKEN:xyz\n"
	if err := os.WriteFile(filepath.Join(rulePath, "house.mdc"), []byte(always), 0o644); err != nil {
		t.Fatal(err)
	}
	globbed := "---\nalwaysApply: true\nglobs: ['**/*.go']\n---\nRULE_GLOB_TOKEN:xyz\n"
	if err := os.WriteFile(filepath.Join(rulePath, "go.mdc"), []byte(globbed), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	st.ReplaceRulesCatalog(session.DiscoverRules(&config.Config{}, tmp))
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	a := NewAgent(cfg, st, nil, nil)
	prompt := a.buildSystemPrompt("agent", nil, nil)
	if !strings.Contains(prompt, "## Active project rules") || !strings.Contains(prompt, "RULE_ALWAYS_TOKEN") {
		t.Fatal("expected the always-on rule under the rules heading")
	}
	if strings.Contains(prompt, "## Active Skills") {
		t.Fatal("rule token should be under Rules not Skills heading")
	}
	// A glob rule waits for its path and then rides in the message or the
	// tool result that brought it, never in the system prompt.
	if strings.Contains(prompt, "RULE_GLOB_TOKEN") {
		t.Fatal("a glob rule reached the system prompt")
	}
}

// A mention-only rule never enters the system prompt. The user names it, its
// body rides in that user's message, and the system message stays byte for byte
// what it was - the provider's cached copy of the conversation behind it holds.
func TestMentionOnlyRuleRidesInTheUserMessage(t *testing.T) {
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
	before := a.buildSystemPrompt("agent", nil, nil)
	if strings.Contains(before, "RULE_MENTION_ONLY") {
		t.Fatal("mention-only rule must not appear without @mention")
	}
	for _, typed := range []string{"please @mention_demo now", "please @rule:mention_demo now"} {
		blocks := (*session.Manager)(nil).ResolvePromptMentions(context.Background(), st, []acp.ContentBlock{{Type: "text", Text: typed}}, session.MentionScope{})
		msg := contentBlocksToText(blocks)
		if !strings.Contains(msg, "RULE_MENTION_ONLY") || !strings.Contains(msg, `kind="rule"`) {
			t.Fatalf("%q: the rule must ride in the user message, got:\n%s", typed, msg)
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: msg})
		if after := a.buildSystemPrompt("agent", nil, nil); after != before {
			t.Fatalf("%q: the system prompt moved:\n--- before\n%s\n--- after\n%s", typed, before, after)
		}
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
	prompt := a.buildSystemPrompt("agent", nil, nil)
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

// An explicit /name invocation carries the skill's body in the message that
// invoked it, written once, so a later turn replays the same bytes instead of
// the message without the body (which cost the provider's cached prefix).
func TestInvokedSkillBlocks_bodyAttached(t *testing.T) {
	const body = "UNIQUE_FIND_SKILLS_BODY_TOKEN"
	sk := &skills.Skill{
		Name:        "SKILL",
		FilePath:    filepath.Join("skills", "find-skills", "SKILL.md"),
		Description: "find skills",
		Content:     body,
	}
	blocks := invokedSkillBlocks("/find-skills search pdf", []*skills.Skill{sk})
	if len(blocks) != 1 || blocks[0].Resource == nil || blocks[0].Resource.Text != body ||
		blocks[0].Resource.URI != "skill:find-skills" || blocks[0].Resource.Mention.Kind != mention.KindSkill {
		t.Fatalf("expected one skill attachment carrying the body, got %+v", blocks)
	}
	msg := contentBlocksToText(append([]acp.ContentBlock{{Type: "text", Text: "/find-skills search pdf"}}, blocks...))
	if !strings.Contains(msg, `kind="skill"`) || !strings.HasPrefix(msg, "/find-skills search pdf") {
		t.Fatalf("the typed text comes first and the body rides as an attachment:\n%s", msg)
	}
	if got := mention.ForDisplay(msg); got != "/find-skills search pdf" {
		t.Fatalf("the transcript shows the message as typed, got %q", got)
	}
}

// Whatever form a mention was typed in, the transcript shows the message as
// typed: the attachment it became is left out of the display, never shown a
// second time under the text with its range repeated.
func TestMentionsDisplayAsTyped(t *testing.T) {
	root := t.TempDir()
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	body := strings.Join(lines, "\n")
	_ = os.WriteFile(filepath.Join(root, "f.go"), []byte(body), 0o644)
	_ = os.MkdirAll(filepath.Join(root, "my folder"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "my folder", "a b.go"), []byte(body), 0o644)
	for _, typed := range []string{
		"see @f.go please",
		"see @f.go:10-20 please",
		"see @f.go#L10-20 please",
		"see @f.go#L10-L20 please",
		"see @f.go#L12 please",
		"see @f.go#10-20 please",
		"see @f.go#12 please",
		`see @"my folder/" please`,
		`see @"my folder/a b.go" please`,
		`see @"my folder/a b.go":3-4 please`,
	} {
		blocks, err := session.HydratePromptContentBlocks(root, []acp.ContentBlock{{Type: acp.ContentTypeText, Text: typed}})
		if err != nil {
			t.Fatal(err)
		}
		if len(blocks) != 2 {
			t.Fatalf("%s: want the text and one attachment, got %d blocks", typed, len(blocks))
		}
		msg := contentBlocksToText(blocks)
		if got := mention.ForDisplay(msg); got != typed {
			t.Fatalf("%s: the transcript shows %q", typed, got)
		}
	}
}

func TestInvokedSkillBlocks_noSkillMatch(t *testing.T) {
	sk := &skills.Skill{
		Name:     "SKILL",
		FilePath: filepath.Join("skills", "other", "SKILL.md"),
		Content:  "other body",
	}
	if blocks := invokedSkillBlocks("/find-skills pdf", []*skills.Skill{sk}); len(blocks) != 0 {
		t.Fatalf("expected nothing when no skill matches; got %+v", blocks)
	}
}

func TestInvokedSkillBlocks_noSlashCommand(t *testing.T) {
	sk := &skills.Skill{
		Name:     "SKILL",
		FilePath: filepath.Join("skills", "find-skills", "SKILL.md"),
		Content:  "body",
	}
	if blocks := invokedSkillBlocks("поищи что-нибудь", []*skills.Skill{sk}); len(blocks) != 0 {
		t.Fatalf("expected nothing without a slash command; got %+v", blocks)
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

	result := buildSkillsPromptMarkdown(allLoaded, active, false)

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

	result := buildSkillsPromptMarkdown(allLoaded, active, false)

	if !strings.Contains(result, body) {
		t.Fatalf("always-apply non-catalog skill body should be in system prompt; got:\n%s", result)
	}
}

// TestBuildSkillsPromptMarkdown_loadSkillHintGatedByAutoDiscovery verifies the
// load_skill hint appears next to the slash catalog only when auto-discovery is on.
func TestBuildSkillsPromptMarkdown_loadSkillHintGatedByAutoDiscovery(t *testing.T) {
	sk := &skills.Skill{
		Name:        "SKILL",
		FilePath:    filepath.Join("skills", "code-review", "SKILL.md"),
		Description: "review code",
		Content:     "body",
	}
	loaded := []*skills.Skill{sk}
	active := skills.FilterForContext(loaded, nil)

	if on := buildSkillsPromptMarkdown(loaded, active, true); !strings.Contains(on, "load_skill") {
		t.Fatalf("auto-discovery on: expected load_skill hint; got:\n%s", on)
	}
	if off := buildSkillsPromptMarkdown(loaded, active, false); strings.Contains(off, "load_skill") {
		t.Fatalf("auto-discovery off: hint must be absent; got:\n%s", off)
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
	for _, want := range []string{"read", "glob", "grep", "websearch", "webfetch", "run_command", "question", "plan_write", "plan_list", "plan_read", "coddy_docs_search", "coddy_docs_read"} {
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

func TestAskToolSetFiltersToReadAndWeb(t *testing.T) {
	r := tools.NewRegistry()
	set := ToolSetForMode("ask")
	filtered := FilterToolDefinitions(r.AllToolDefinitions(), set)
	got := make(map[string]bool)
	for _, d := range filtered {
		got[d.Name] = true
	}
	for _, want := range []string{"read", "keep_result", "glob", "grep", "print_tree", "websearch", "webfetch", "question", "coddy_docs_search", "coddy_docs_read"} {
		if !got[want] {
			t.Errorf("ask toolset should include %q", want)
		}
	}
	for _, forbid := range []string{"write", "edit", "apply_patch", "run_command", "background_list", "plan_write", "plan_list", "plan_read", "config_get", "config_set", "coddy_todo_plan_read"} {
		if got[forbid] {
			t.Errorf("ask toolset should not include %q", forbid)
		}
	}
}

func TestToolCallRefusedByModeEnforcesAskOnly(t *testing.T) {
	if msg, refused := toolCallRefusedByMode("ask", "write"); !refused || !strings.Contains(msg, "Ask mode") {
		t.Errorf("ask mode must refuse write at execution time, got refused=%v msg=%q", refused, msg)
	}
	if _, refused := toolCallRefusedByMode("ask", "mcp_server__lookup"); !refused {
		t.Error("ask mode must refuse MCP tool calls at execution time")
	}
	if _, refused := toolCallRefusedByMode("ask", "read"); refused {
		t.Error("ask mode must allow read")
	}
	for _, mode := range []string{"agent", "plan"} {
		if _, refused := toolCallRefusedByMode(mode, "write"); refused {
			t.Errorf("%s mode must not enforce the execution-time refusal", mode)
		}
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

	res, err := ag.CompactSession(context.Background(), CompactOptions{Instructions: "focus on file paths"})
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

func TestCompactSessionPrunesHeadUsingWritesFromKeptTail(t *testing.T) {
	st := &session.State{
		ID:   "sess_compact_stale_read",
		CWD:  testCWD,
		Mode: session.ModeAgent,
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "inspect the file"})
	st.AddMessage(asstRead("read", "big.go", 1, 500, true))
	st.AddMessage(toolResult("read", bigBody("STALE FILE CONTENT")))
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "now change it"})
	st.AddMessage(asstWrite("write", "write", "big.go"))
	st.AddMessage(toolResult("write", "written"))

	keepTurns := 1
	keepResults := 0
	minBytes := 10
	provider := &compactCannedProvider{t: t, summary: "dense summary"}
	ag := compactTestAgent(t, st, config.Compaction{
		KeepRecentTurns: &keepTurns,
		ResultEviction: config.ResultEviction{
			KeepRecent:     &keepResults,
			MinResultBytes: &minBytes,
		},
	}, provider)

	if _, err := ag.CompactSession(context.Background(), CompactOptions{}); err != nil {
		t.Fatal(err)
	}
	request := transcriptText(provider.requests[0])
	if strings.Contains(request, "STALE FILE CONTENT") {
		t.Fatalf("compaction request retained a read made stale by a write in the kept tail:\n%s", request)
	}
	if !strings.Contains(request, "modified after this read") {
		t.Fatalf("compaction request missing stale-read placeholder:\n%s", request)
	}
}

func TestCompactSessionNothingToCompact(t *testing.T) {
	// One user turn: the prompt being answered, which an automatic compaction
	// never folds, whatever keep_recent_turns says.
	st := seededCompactState(t, 1)
	keep := 2
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, &compactCannedProvider{t: t, summary: "s"})

	if _, err := ag.CompactSession(context.Background(), CompactOptions{}); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("err = %v, want ErrNothingToCompact", err)
	}
	if len(st.GetMessages()) != 2 {
		t.Fatal("history must stay untouched")
	}
}

func TestCompactSessionAutoKeepsFewerTurnsWhenTheTailCoversEveryTurn(t *testing.T) {
	// keep_recent_turns 2 over a window of exactly 2 user turns: a long session
	// of a few big agent turns. The automatic trigger folds the older turn and
	// keeps the latest one verbatim instead of skipping.
	st := seededCompactState(t, 2)
	keep := 2
	provider := &compactCannedProvider{t: t, summary: "folded first turn"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)

	res, err := ag.CompactSession(context.Background(), CompactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.CompactedMessages != 2 || res.KeptMessages != 2 {
		t.Fatalf("counts = %d/%d, want 2/2", res.CompactedMessages, res.KeptMessages)
	}
	msgs := st.GetMessages()
	if len(msgs) != 5 || !msgs[2].CompactionSummary {
		t.Fatalf("summary not inserted before the latest turn: %+v", msgs)
	}
	if msgs[3].Role != llm.RoleUser || msgs[3].Content != "question 2" {
		t.Fatalf("latest prompt not kept verbatim after the summary: %+v", msgs[3])
	}
	if req := transcriptText(provider.requests[0]); strings.Contains(req, "question 2") {
		t.Fatalf("the latest prompt leaked into the summarization request:\n%s", req)
	}
}

func TestCompactSessionDisabled(t *testing.T) {
	st := seededCompactState(t, 3)
	off := false
	ag := compactTestAgent(t, st, config.Compaction{Enabled: &off}, &compactCannedProvider{t: t, summary: "s"})

	if _, err := ag.CompactSession(context.Background(), CompactOptions{}); err == nil {
		t.Fatal("disabled compaction must error")
	}
}

func TestCompactSessionEmptySummaryFails(t *testing.T) {
	st := seededCompactState(t, 3)
	keep := 1
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, &compactCannedProvider{t: t, summary: "   "})

	if _, err := ag.CompactSession(context.Background(), CompactOptions{}); err == nil {
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

	if _, err := ag.CompactSession(context.Background(), CompactOptions{}); err == nil {
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
		// The separators draftCommandArg.ts mirrors: a no-break space is not one.
		{in: "/compact\u00a0--model x", wantOK: false},
		{in: "hello /compact", wantOK: false},
		{in: "", wantOK: false},
	}
	for _, tc := range cases {
		args, ok := parseCompactCommand(tc.in)
		arg := args.Instructions
		if ok != tc.wantOK || arg != tc.wantArg {
			t.Errorf("parseCompactCommand(%q) = (%q, %v), want (%q, %v)", tc.in, arg, ok, tc.wantArg, tc.wantOK)
		}
	}
}

func TestRunCompactCommandPersistsUserMessage(t *testing.T) {
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
	foundCmd := false
	for _, m := range msgs {
		if m.Role == llm.RoleUser && strings.TrimSpace(m.Content) == "/compact focus on tests" {
			foundCmd = true
		}
	}
	if !foundCmd {
		t.Fatalf("the /compact command must be persisted as a user message so it shows in the transcript: %+v", msgs)
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

// TestRunCompactCommandForcesShortChat covers ask: manual /compact must fold even
// a very short conversation (below the keep-recent boundary), not refuse it.
func TestRunCompactCommandForcesShortChat(t *testing.T) {
	st := seededCompactState(t, 1) // one exchange (1 user turn) — below keep=2
	keep := 2
	provider := &compactCannedProvider{t: t, summary: "short summary"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "/compact"}})
	if err != nil {
		t.Fatal(err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop = %q", stop)
	}
	msgs := st.GetMessages()
	sawSummary := false
	for _, m := range msgs {
		if m.CompactionSummary {
			sawSummary = true
		}
	}
	if !sawSummary {
		t.Fatalf("forced /compact must summarize even a short chat: %+v", msgs)
	}
}

func TestRunCompactCommandNothingToCompact(t *testing.T) {
	st := seededCompactState(t, 0) // empty history: even forced compaction has nothing to fold
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
		// A model without max_context_tokens measures against the default
		// window, the one GET /v1/models hands the web UI ring (#245).
		{name: "no max_context_tokens at the default window threshold", est: 102400, maxContext: 0, enabled: true, wantCompact: true},
		{name: "no max_context_tokens below the default window threshold", est: 102399, maxContext: 0, enabled: true, wantCompact: false},
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

// windowedState is a session whose manager resolved a context window from the
// provider's model listing (session.State.ContextWindow).
type windowedState struct {
	*session.State
	window int
}

func (w windowedState) ContextWindow(*config.Config) (int, string) {
	return w.window, session.ContextWindowFromProvider
}

func TestMaybeAutoCompactMeasuresAgainstTheSessionWindow(t *testing.T) {
	st := seededCompactState(t, 3)
	keep := 1
	provider := &compactCannedProvider{t: t, summary: "auto summary"}
	// max_context_tokens stays unset: the window comes from the session, which
	// read it from the provider's listing.
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)
	ag.state = windowedState{State: st, window: 1000}
	st.SetLastContextBreakdown(&session.ContextBreakdown{EstimatedTotal: 800})

	if !ag.maybeAutoCompact(context.Background()) {
		t.Fatal("80% of the session's 1000-token window must compact")
	}
	if len(provider.requests) != 1 {
		t.Fatalf("summarizer called %d times, want 1", len(provider.requests))
	}
}

func TestMaybeAutoCompactLogsOnceWhenNothingCanBeFolded(t *testing.T) {
	st := &session.State{ID: "sess_compact_one_turn", CWD: t.TempDir(), Mode: session.ModeAgent}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "the only prompt, still being answered"})
	var logs bytes.Buffer
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}, st, resumePermissionSender{}, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return &compactCannedProvider{t: t, summary: "must not be asked"}, nil
	}
	st.SetLastContextBreakdown(&session.ContextBreakdown{EstimatedTotal: 95})

	for i := 0; i < 3; i++ {
		if ag.maybeAutoCompact(context.Background()) {
			t.Fatal("the prompt being answered must never be folded")
		}
	}
	if got := strings.Count(logs.String(), "auto-compaction skipped"); got != 1 {
		t.Fatalf("skip logged %d times in one turn, want once:\n%s", got, logs.String())
	}
	for _, m := range st.GetMessages() {
		if m.CompactionSummary {
			t.Fatal("no summary row expected")
		}
	}
}

func TestResumeAfterPermissionAutoCompactsBeforeFirstLLMCall(t *testing.T) {
	st := seededCompactState(t, 3)
	st.SessionDir = t.TempDir()
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "run the blocked command"})
	st.AddMessage(llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{
			ID:        "call_blocked_compact",
			Name:      "run_command",
			InputJSON: `{"command":"printf SHOULD_NOT_RUN"}`,
		}},
	})
	provider := &autoCompactRunProvider{}
	keep := 1
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		// Tiny window: the system prompt alone exceeds 80% of 50 tokens.
		Models:     []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 50}},
		Agent:      config.Agent{Model: "fake/model"},
		Compaction: config.Compaction{KeepRecentTurns: &keep},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return provider, nil
	}

	if _, err := ag.ResumeAfterPermission(context.Background(), "call_blocked_compact", &acp.PermissionResult{
		Outcome:  "cancelled",
		OptionID: "reject",
	}); err != nil {
		t.Fatal(err)
	}
	if len(provider.streamSeen) == 0 {
		t.Fatal("no stream request recorded")
	}
	sawSummary := false
	for _, m := range provider.streamSeen[0] {
		if m.Role == llm.RoleSystem {
			continue
		}
		if !m.CompactionSummary {
			t.Fatalf("first LLM request after the resume does not start from the summary: %+v", m)
		}
		sawSummary = true
		break
	}
	if !sawSummary {
		t.Fatal("summary missing from the first LLM request after the resume")
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

// --- loop guard escalation and false-positive safety -----------------------

// alwaysDegeneratingProvider never recovers: every turn degenerates into the same
// repeated passage. The loop guard must give up after the nudge budget instead of
// nudging forever.
type alwaysDegeneratingProvider struct{ calls int }

func (p *alwaysDegeneratingProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, nil
}

func (p *alwaysDegeneratingProvider) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	var produced strings.Builder
	for i := 0; i < 200 && ctx.Err() == nil; i++ {
		produced.WriteString(bddLoopedSentence)
		onChunk(llm.StreamChunk{TextDelta: bddLoopedSentence})
	}
	return &llm.Response{Content: produced.String(), StopReason: "tool_use"}, context.Canceled
}

func TestLoopGuardStopsTurnAfterNudgeBudget(t *testing.T) {
	st := &session.State{
		ID:         "sess_loop_budget",
		CWD:        t.TempDir(),
		Mode:       session.ModeAgent,
		SessionDir: t.TempDir(),
	}
	provider := &alwaysDegeneratingProvider{}
	nudges := 2
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 20, LoopNudgeMax: &nudges},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "do the thing"}})
	if err == nil {
		t.Fatal("expected the turn to stop with a notice once the nudge budget ran out")
	}
	if !strings.Contains(err.Error(), "repeating") {
		t.Fatalf("error should explain the loop: %v", err)
	}
	if stop != string(acp.StopReasonRefused) {
		t.Fatalf("stop = %q, want agent_refused", stop)
	}
	// One initial attempt plus one per nudge, and nowhere near max_turns.
	if provider.calls != nudges+1 {
		t.Fatalf("provider called %d times, want %d (initial attempt + %d nudges)", provider.calls, nudges+1, nudges)
	}
}

func TestLoopGuardDisabledLetsTheStreamRun(t *testing.T) {
	st := &session.State{
		ID:         "sess_loop_off",
		CWD:        t.TempDir(),
		Mode:       session.ModeAgent,
		SessionDir: t.TempDir(),
	}
	provider := &bddLoopProvider{
		channel:      loopAbortText,
		recoverAfter: 0,
		realAnswer:   "answered without interference",
		maxDeltas:    30,
	}
	off := false
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", LoopGuard: &off},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }

	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "go"}}); err != nil {
		t.Fatalf("turn failed with the guard disabled: %v", err)
	}
	if provider.cancelled != 0 {
		t.Fatal("the guard cancelled a stream even though loop_guard is false")
	}
}

// varyingToolProvider calls the same tool with different arguments every turn.
// That is ordinary progress, not a loop, and must never be blocked.
type varyingToolProvider struct{ calls int }

func (p *varyingToolProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, nil
}

func (p *varyingToolProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	if p.calls > 6 {
		onChunk(llm.StreamChunk{TextDelta: "done"})
		return &llm.Response{Content: "done", StopReason: "end_turn"}, nil
	}
	tc := llm.ToolCall{
		ID:        fmt.Sprintf("call_%d", p.calls),
		Name:      "glob",
		InputJSON: fmt.Sprintf(`{"pattern":"**/*%d.go"}`, p.calls),
	}
	onChunk(llm.StreamChunk{ToolCall: &tc})
	return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}, nil
}

func TestLoopGuardIgnoresVaryingToolArguments(t *testing.T) {
	st := &session.State{
		ID:         "sess_loop_varying",
		CWD:        t.TempDir(),
		Mode:       session.ModeAgent,
		SessionDir: t.TempDir(),
	}
	provider := &varyingToolProvider{}
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 20},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "search for things"}})
	if err != nil {
		t.Fatalf("the guard interfered with legitimate varying tool calls: %v", err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop = %q, want end_turn", stop)
	}
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleTool && (m.Content == toolLoopNudge || m.Content == toolLoopSkippedResult) {
			t.Fatal("the loop guard blocked a call with different arguments")
		}
	}
}

// A pending agent-mode call approved after the session switched to ask must be
// refused, and an "allow always" answer must not leave a grant behind for the
// call that never ran.
func TestResumeAfterPermissionInAskModeRefusesAndRecordsNoGrant(t *testing.T) {
	st := &session.State{
		ID:         "sess_resume_ask",
		CWD:        t.TempDir(),
		Mode:       session.ModeAsk,
		SessionDir: t.TempDir(),
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "run it"},
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:        "call_ask_hidden",
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

	// Every persisted bundle carries the arguments the prompt showed; a
	// resume without them fails closed before the mode check.
	if err := session.WriteToolCallArgs(st.SessionDir, "call_ask_hidden", `{"command":"printf SHOULD_NOT_RUN"}`); err != nil {
		t.Fatal(err)
	}
	stop, err := ag.ResumeAfterPermission(context.Background(), "call_ask_hidden", &acp.PermissionResult{
		Outcome:  "allow",
		OptionID: "allow_always",
	})
	if err != nil {
		t.Fatal(err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop reason %q", stop)
	}
	var toolMsg *llm.Message
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == "call_ask_hidden" {
			mm := m
			toolMsg = &mm
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("missing tool result for the refused call")
	}
	if strings.Contains(toolMsg.Content, "SHOULD_NOT_RUN") {
		t.Fatalf("the approved call executed in ask mode: %q", toolMsg.Content)
	}
	if !strings.Contains(toolMsg.Content, "not available in Ask mode") {
		t.Fatalf("tool result is not the ask-mode refusal: %q", toolMsg.Content)
	}
	if grants := st.GetPermissionCommandGrants(); len(grants) != 0 {
		t.Fatalf("refused call still recorded an allow-always grant: %v", grants)
	}
}

func TestContentBlocksToText_lineRangeAttachment(t *testing.T) {
	blocks := []acp.ContentBlock{
		{Type: "resource", Resource: &acp.Resource{URI: "docs/ui.md#L10-20", Text: "body"}},
	}
	got := contentBlocksToText(blocks)
	if !strings.Contains(got, `path="docs/ui.md"`) ||
		!strings.Contains(got, `name="ui.md"`) ||
		!strings.Contains(got, `lines="10-20"`) {
		t.Fatalf("unexpected XML bundle: %s", got)
	}
	if strings.Contains(got, "#L10-20") {
		t.Fatalf("range fragment leaked into the path: %s", got)
	}
}

// A path that is not a well-formed range fragment stays part of the file name.
func TestContentBlocksToText_noLinesAttributeWithoutRange(t *testing.T) {
	blocks := []acp.ContentBlock{
		{Type: "resource", Resource: &acp.Resource{URI: "notes.md", Text: "b"}},
	}
	if got := contentBlocksToText(blocks); strings.Contains(got, "lines=") {
		t.Fatalf("unexpected lines attribute: %s", got)
	}
}

// resumeRewriteFixture prepares a session whose pending run_command call was
// rewritten by a PreToolUse hook that then asked for permission: the history
// holds the model's original arguments, the bundle holds the arguments the
// prompt showed, exactly the state a persisted approval resumes from.
func resumeRewriteFixture(t *testing.T, hookCommand string) (*Agent, *session.State, string) {
	t.Helper()
	home := t.TempDir()
	if err := hooktest.Write(filepath.Join(home, "hooks.json"), hooktest.Entry{
		Event:    hooks.EventPreToolUse,
		Matcher:  "run_command",
		Handlers: []hooks.Handler{hooktest.Handler("rewrite-ask", hookCommand)},
	}); err != nil {
		t.Fatal(err)
	}
	st := &session.State{
		ID:         "sess_resume_rewrite",
		CWD:        t.TempDir(),
		Mode:       session.ModeAgent,
		SessionDir: t.TempDir(),
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "run the command"},
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:        "call_rewrite",
					Name:      "run_command",
					InputJSON: `{"command":"echo original-arguments"}`,
				}},
			},
		},
	}
	// What the permission prompt showed: the arguments after the first
	// PreToolUse run, persisted by executeToolCall before the prompt.
	if err := session.WriteToolCallArgs(st.SessionDir, "call_rewrite", `{"command":"echo shown-and-approved"}`); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: st.CWD},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	cfg.Hooks.ApplyDefaults(cfg.Paths)
	provider := &resumePermissionProvider{t: t}
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	return ag, st, home
}

func resumedToolResult(st *session.State) string {
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == "call_rewrite" {
			return m.Content
		}
	}
	return ""
}

// The approval binds to the arguments the prompt showed: they run even when
// the hook that produced them is gone by the time the approval arrives.
func TestResumeAfterPermissionRunsTheApprovedArguments(t *testing.T) {
	ag, st, home := resumeRewriteFixture(t, "echo shown-and-approved")
	if err := os.Remove(filepath.Join(home, "hooks.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.ResumeAfterPermission(context.Background(), "call_rewrite", &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}); err != nil {
		t.Fatal(err)
	}
	result := resumedToolResult(st)
	if !strings.Contains(result, "shown-and-approved") || strings.Contains(result, "original-arguments") {
		t.Fatalf("the resumed call must run the approved arguments, got %q", result)
	}
	if grants := st.GetPermissionCommandGrants(); len(grants) != 0 {
		t.Fatalf("a plain allow records no grant, got %v", grants)
	}
}

// A hook that changes the approved arguments again on the resume is not
// covered by the answer the user gave: the call is cancelled instead.
func TestResumeAfterPermissionRefusesArgumentsChangedAfterTheApproval(t *testing.T) {
	ag, st, _ := resumeRewriteFixture(t, "echo changed-after-approval")
	if _, err := ag.ResumeAfterPermission(context.Background(), "call_rewrite", &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}); err != nil {
		t.Fatal(err)
	}
	result := resumedToolResult(st)
	if strings.Contains(result, "changed-after-approval") || strings.Contains(result, "shown-and-approved") || !strings.Contains(result, "cancelled") {
		t.Fatalf("a call rewritten after the approval must not run, got %q", result)
	}
}

// promptRefusingSender fails the test if a permission prompt is issued.
type promptRefusingSender struct {
	t        *testing.T
	prompted bool
}

func (*promptRefusingSender) SendSessionUpdate(string, interface{}) error { return nil }

func (s *promptRefusingSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.prompted = true
	s.t.Error("no permission prompt must be issued")
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}

func (*promptRefusingSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

// toolCallArgsPath finds the persisted args.json of the fixture's tool call.
func toolCallArgsPath(t *testing.T, sessionDir string) string {
	t.Helper()
	var found string
	_ = filepath.WalkDir(sessionDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "args.json" {
			found = p
		}
		return nil
	})
	if found == "" {
		t.Fatalf("no persisted arguments under %s", sessionDir)
	}
	return found
}

// The same hook answering again on the resume is not a change: the bundle
// stores the arguments pretty-printed and the hook answers them compact, and
// the approval must survive that formatting difference.
func TestResumeAfterPermissionRunsWhenTheSameHookAnswersAgain(t *testing.T) {
	ag, st, _ := resumeRewriteFixture(t, "echo shown-and-approved")
	if _, err := ag.ResumeAfterPermission(context.Background(), "call_rewrite", &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}); err != nil {
		t.Fatal(err)
	}
	result := resumedToolResult(st)
	if !strings.Contains(result, "shown-and-approved") || strings.Contains(result, "cancelled") {
		t.Fatalf("the hook that produced the approved arguments must not cancel the resume, got %q", result)
	}
}

// A bundle without persisted arguments has nothing the approval can bind to:
// the resume fails instead of running the history's arguments.
func TestResumeAfterPermissionFailsClosedWithoutPersistedArguments(t *testing.T) {
	ag, st, home := resumeRewriteFixture(t, "echo shown-and-approved")
	if err := os.Remove(filepath.Join(home, "hooks.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(toolCallArgsPath(t, st.SessionDir)); err != nil {
		t.Fatal(err)
	}
	_, err := ag.ResumeAfterPermission(context.Background(), "call_rewrite", &acp.PermissionResult{Outcome: "selected", OptionID: "allow"})
	if err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("a resume without persisted arguments must fail, got %v", err)
	}
	if result := resumedToolResult(st); result != "" {
		t.Fatalf("nothing must run without persisted arguments, got %q", result)
	}
}

// A refusal needs nothing from the bundle: it is recorded and the gate is
// cleared even when the persisted arguments cannot be read.
func TestResumeAfterPermissionRejectsWithoutReadingTheArguments(t *testing.T) {
	ag, st, _ := resumeRewriteFixture(t, "echo shown-and-approved")
	if err := session.WritePendingPermission(st.SessionDir, acp.PermissionRequestParams{
		SessionID: st.ID,
		ToolCall:  acp.PermissionToolCall{ToolCallID: "call_rewrite", Status: "pending"},
	}, "run_command", ""); err != nil {
		t.Fatal(err)
	}
	p := toolCallArgsPath(t, st.SessionDir)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.ResumeAfterPermission(context.Background(), "call_rewrite", &acp.PermissionResult{Outcome: "selected", OptionID: "reject"}); err != nil {
		t.Fatalf("a refusal must not depend on the arguments file: %v", err)
	}
	if result := resumedToolResult(st); result != "permission denied by user" {
		t.Fatalf("the refusal must be recorded, got %q", result)
	}
	if session.PendingPermissionHeld(st.SessionDir) {
		t.Fatal("the refused gate must be cleared")
	}
}

// Persisted arguments that cannot be read fail closed: nothing runs, and the
// error leaves the pending gate in place for another attempt.
func TestResumeAfterPermissionFailsClosedWhenTheApprovedArgumentsCannotBeRead(t *testing.T) {
	ag, st, _ := resumeRewriteFixture(t, "echo shown-and-approved")
	p := toolCallArgsPath(t, st.SessionDir)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	// A directory in place of the file: the read fails, and not with not-exist.
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ag.ResumeAfterPermission(context.Background(), "call_rewrite", &acp.PermissionResult{Outcome: "selected", OptionID: "allow"})
	if err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("an unreadable arguments file must fail the resume, got %v", err)
	}
	if result := resumedToolResult(st); result != "" {
		t.Fatalf("nothing must run when the approved arguments cannot be read, got %q", result)
	}
}

// Rewritten arguments that cannot be persisted cancel the call before the
// prompt: a resume would otherwise fall back to arguments the user never saw.
func TestRewrittenArgumentsThatCannotBePersistedCancelBeforeThePrompt(t *testing.T) {
	ag, st, _ := resumeRewriteFixture(t, "echo shown-and-approved")
	toolCalls := filepath.Dir(filepath.Dir(toolCallArgsPath(t, st.SessionDir)))
	if err := os.RemoveAll(toolCalls); err != nil {
		t.Fatal(err)
	}
	// A file where the tool call directories live: every write fails.
	if err := os.WriteFile(toolCalls, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	sender := &promptRefusingSender{t: t}
	ag.server = sender
	tc := st.GetMessages()[1].ToolCalls[0]
	env := ag.buildToolEnv(st.GetMode(), st.SessionDir)
	result, err := ag.executeToolCall(context.Background(), tc, env, st.GetMode(), st.GetID(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "cancelled") || !strings.Contains(result, "persisted") {
		t.Fatalf("a rewrite that cannot be persisted must cancel the call, got %q", result)
	}
	if sender.prompted {
		t.Fatal("the prompt must not be issued for arguments that were not persisted")
	}
}

// The comparison behind the resume check keeps number literals verbatim: a
// float64 decode would read two integers past 2^53 as the same arguments.
func TestSameToolArgsKeepsLargeIntegersApart(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		same bool
	}{
		{"formatting", `{"command":"echo x","n":1}`, "{\n  \"n\": 1,\n  \"command\": \"echo x\"\n}\n", true},
		{"large integers", `{"n":9007199254740992}`, `{"n":9007199254740993}`, false},
		{"float literal", `{"n":1.0}`, `{"n":1}`, false},
		{"different values", `{"command":"echo a"}`, `{"command":"echo b"}`, false},
		{"invalid", `{not json`, `{not json`, true},
		{"one invalid", `{"n":1}`, `{n:1}`, false},
	}
	for _, c := range cases {
		if got := sameToolArgs(c.a, c.b); got != c.same {
			t.Errorf("%s: sameToolArgs(%q, %q) = %v, want %v", c.name, c.a, c.b, got, c.same)
		}
	}
}

// --- /export built-in command ---------------------------------------------

func TestParseExportCommand(t *testing.T) {
	tests := []struct {
		in     string
		want   exportCommandArgs
		wantOK bool
	}{
		{"/export", exportCommandArgs{}, true},
		{"  /export  ", exportCommandArgs{}, true},
		{"/export md", exportCommandArgs{Format: "md"}, true},
		{"/export JSON chat.json", exportCommandArgs{Format: "JSON", Target: "chat.json"}, true},
		{"/export chat.md", exportCommandArgs{Target: "chat.md"}, true},
		{"/export md my notes/chat.md", exportCommandArgs{Format: "md", Target: "my notes/chat.md"}, true},
		{"/export\thtml\tout/", exportCommandArgs{Format: "html", Target: "out/"}, true},
		{"/export\rjsonl", exportCommandArgs{Format: "jsonl"}, true},
		{"/export --no-tools md chat.md", exportCommandArgs{Format: "md", Target: "chat.md", Options: session.ExportOptions{NoTools: true}}, true},
		{"/export md chat.md --no-thinking", exportCommandArgs{Format: "md", Target: "chat.md", Options: session.ExportOptions{NoThinking: true}}, true},
		{"/export --no-tools --no-thinking", exportCommandArgs{Options: session.ExportOptions{NoTools: true, NoThinking: true}}, true},
		{"/export --bogus chat.md", exportCommandArgs{Target: "chat.md", UnknownOptions: []string{"--bogus"}}, true},
		{"/exports", exportCommandArgs{}, false},          // must not match a longer word
		{"say /export later", exportCommandArgs{}, false}, // only when it leads the message
		{"hello world", exportCommandArgs{}, false},
	}
	for _, tc := range tests {
		got, ok := parseExportCommand(tc.in)
		if ok != tc.wantOK {
			t.Errorf("parseExportCommand(%q) ok=%v, want %v", tc.in, ok, tc.wantOK)
			continue
		}
		if ok && !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseExportCommand(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

// newExportTestAgent builds an agent over a two-message session whose provider
// factory fails: a built-in command must never reach the model.
func newExportTestAgent(t *testing.T) (*Agent, *session.State, *compactionUsageSender, string) {
	t.Helper()
	cwd := t.TempDir()
	st := &session.State{
		ID:         "sess_export",
		CWD:        cwd,
		Mode:       session.ModeAgent,
		SessionDir: t.TempDir(),
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "question 1", CreatedAt: "2026-09-06T10:00:00Z"})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "answer 1", Model: "fake/model", CreatedAt: "2026-09-06T10:00:01Z"})
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	sender := &compactionUsageSender{}
	ag := NewAgent(cfg, st, sender, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return nil, errors.New("the LLM must not be called for /export")
	}
	return ag, st, sender, cwd
}

func lastAssistantText(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant {
			return msgs[i].Content
		}
	}
	return ""
}

func TestRunExportCommandWritesTranscriptAndPersistsRows(t *testing.T) {
	ag, st, sender, cwd := newExportTestAgent(t)

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "/export md chat.md"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop reason = %q", stop)
	}

	b, err := os.ReadFile(filepath.Join(cwd, "chat.md"))
	if err != nil {
		t.Fatalf("exported file: %v", err)
	}
	md := string(b)
	for _, want := range []string{"`sess_export`", "question 1", "answer 1", "`fake/model`"} {
		if !strings.Contains(md, want) {
			t.Errorf("export lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "/export") {
		t.Errorf("the export must hold the conversation before the command, got:\n%s", md)
	}

	msgs := st.GetMessages()
	if len(msgs) != 4 {
		t.Fatalf("transcript rows = %d, want 4 (command + reply appended)", len(msgs))
	}
	if msgs[2].Role != llm.RoleUser || msgs[2].Content != "/export md chat.md" {
		t.Fatalf("command row = %+v", msgs[2])
	}
	reply := lastAssistantText(msgs)
	if !strings.HasPrefix(reply, "Session exported to markdown: chat.md") {
		t.Fatalf("reply = %q", reply)
	}
	if !strings.Contains(reply, filepath.Join(cwd, "chat.md")) {
		t.Fatalf("reply does not name the full path: %q", reply)
	}
	streamed := false
	for _, u := range sender.updates {
		if chunk, ok := u.(acp.MessageChunkUpdate); ok && chunk.Content.Text == reply {
			streamed = true
		}
	}
	if !streamed {
		t.Fatalf("the reply was not streamed as an agent message chunk: %+v", sender.updates)
	}
}

// A built-in reads what the operator typed: data piped under "/export
// chat.md" is not part of the target, so the file is chat.md and nothing
// made of the attachment's words.
func TestBuiltinCommandsReadTypedTextOnly(t *testing.T) {
	ag, _, _, cwd := newExportTestAgent(t)
	stop, err := ag.Run(context.Background(), []acp.ContentBlock{
		{Type: "text", Text: "/export md chat.md"},
		session.StdinAttachment("piped words that are no path\n"),
	})
	if err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("Run: %v, stop %q", err, stop)
	}
	if _, err := os.Stat(filepath.Join(cwd, "chat.md")); err != nil {
		t.Fatalf("the export did not land on the typed target: %v", err)
	}
	entries, _ := os.ReadDir(cwd)
	for _, e := range entries {
		if e.Name() != "chat.md" && strings.Contains(e.Name(), "piped") {
			t.Fatalf("an export target was built from the attachment: %q", e.Name())
		}
	}
}

func TestRunExportCommandRejectsPathOutsideWorkspace(t *testing.T) {
	ag, st, _, cwd := newExportTestAgent(t)

	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "/export md ../escape.md"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	reply := lastAssistantText(st.GetMessages())
	if !strings.Contains(reply, "inside the session workspace") {
		t.Fatalf("reply = %q", reply)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(cwd), "escape.md")); !os.IsNotExist(err) {
		t.Fatalf("a file escaped the workspace: %v", err)
	}
}

func TestRunExportCommandExplainsUnknownFormat(t *testing.T) {
	ag, st, _, cwd := newExportTestAgent(t)

	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "/export yaml"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	reply := lastAssistantText(st.GetMessages())
	if !strings.Contains(reply, "Usage: /export") || !strings.Contains(reply, "yaml") {
		t.Fatalf("reply = %q", reply)
	}
	if _, err := os.Stat(filepath.Join(cwd, "yaml")); !os.IsNotExist(err) {
		t.Fatalf("a mistyped format became a file: %v", err)
	}

	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "/export --bogus chat.md"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	reply = lastAssistantText(st.GetMessages())
	if !strings.Contains(reply, "unknown option --bogus") || !strings.Contains(reply, "Usage: /export") {
		t.Fatalf("reply = %q", reply)
	}
}

func TestRunExportCommandOptionsTrimToolsAndThinking(t *testing.T) {
	ag, st, _, cwd := newExportTestAgent(t)
	st.AddMessage(llm.Message{
		Role:      llm.RoleAssistant,
		Reasoning: "private thoughts",
		ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read", InputJSON: `{"path":"README.md"}`}},
		Model:     "fake/model",
	})
	st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "call_1", Content: "README BODY"})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "answer 2", Model: "fake/model"})

	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "/export md chat.md --no-tools --no-thinking"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(cwd, "chat.md"))
	if err != nil {
		t.Fatalf("exported file: %v", err)
	}
	md := string(b)
	for _, want := range []string{"question 1", "answer 1", "answer 2"} {
		if !strings.Contains(md, want) {
			t.Errorf("export lacks %q:\n%s", want, md)
		}
	}
	for _, unwanted := range []string{"README BODY", "private thoughts", "Tool call"} {
		if strings.Contains(md, unwanted) {
			t.Errorf("export still holds %q:\n%s", unwanted, md)
		}
	}
}

// The loop tags its own logger, so logger.levels can name "agent" whichever
// entrypoint built it. Without the tag the component key is absent and a
// configured override for "agent" scopes nothing.
func TestNewAgentTagsItsLoggerWithTheAgentComponent(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a := NewAgent(&config.Config{}, &session.State{ID: "sess_probe"}, nil, base)

	a.log.Info("probe")

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
	}
	if got := rec[logger.ComponentKey]; got != logger.ComponentAgent {
		t.Fatalf("component = %v, want %q", got, logger.ComponentAgent)
	}
}

// silentLaneProvider never emits a chunk and returns only when the caller gives
// up, the way a provider behaves against an upstream that accepts the request
// and then sends nothing.
type silentLaneProvider struct{ calls int }

func (p *silentLaneProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, nil
}

func (p *silentLaneProvider) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestFirstTokenGuardGivesUpAfterItsRetryBudget keeps the replay bounded: a lane
// where every member is mute must still end the turn with the notice, not sit in
// a retry loop until max_turns.
func TestFirstTokenGuardGivesUpAfterItsRetryBudget(t *testing.T) {
	st := &session.State{
		ID:         "sess_silent_lane",
		CWD:        t.TempDir(),
		Mode:       session.ModeAgent,
		SessionDir: t.TempDir(),
	}
	provider := &silentLaneProvider{}
	ms := 100
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 20, LLMFirstTokenTimeoutMS: &ms},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "do the thing"}})
	if err == nil || !strings.Contains(err.Error(), "did not respond") {
		t.Fatalf("want the silence notice, got stop %q err %v", stop, err)
	}
	if stop != string(acp.StopReasonRefused) {
		t.Fatalf("stop = %q, want refused", stop)
	}
	if want := maxFirstTokenRetries + 1; provider.calls != want {
		t.Fatalf("provider called %d times, want %d (the first attempt plus its replays)", provider.calls, want)
	}
}

// emptyThenNudgedProvider records what it was sent so a test can tell the plain
// replay from the nudge that follows it.
type emptyThenNudgedProvider struct {
	calls int
	seen  [][]llm.Message
}

func (p *emptyThenNudgedProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, nil
}

func (p *emptyThenNudgedProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	p.seen = append(p.seen, append([]llm.Message(nil), messages...))
	onChunk(llm.StreamChunk{ReasoningDelta: `We should read the file.{"path":"main.go"}`})
	return &llm.Response{Content: "", StopReason: "end_turn"}, nil
}

// TestEmptyTurnNudgesOnlyAfterTheReplayDidNotHelp fixes the order of the two
// recoveries: the identical request goes out first (a sick deployment answers
// differently on the next draw), and only then is the model argued with. It also
// pins the total, so neither recovery is skipped or doubled.
func TestEmptyTurnNudgesOnlyAfterTheReplayDidNotHelp(t *testing.T) {
	st := &session.State{
		ID:         "sess_empty_then_nudge",
		CWD:        t.TempDir(),
		Mode:       session.ModeAgent,
		SessionDir: t.TempDir(),
	}
	provider := &emptyThenNudgedProvider{}
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 20},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }

	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "do the thing"}}); err == nil {
		t.Fatal("expected the no-reply notice once every recovery was spent")
	}

	if want := 1 + maxEmptyAssistantReissues + maxEmptyAssistantContinuations; provider.calls != want {
		t.Fatalf("provider called %d times, want %d (first attempt, replays, nudges)", provider.calls, want)
	}
	carriesNudge := func(msgs []llm.Message) bool {
		for _, m := range msgs {
			if strings.Contains(m.Content, emptyAssistantContinuationNudge) {
				return true
			}
		}
		return false
	}
	if carriesNudge(provider.seen[1]) {
		t.Fatal("the first recovery was a nudge; the plain replay must come first")
	}
	if len(provider.seen[1]) != len(provider.seen[0]) {
		t.Fatalf("the replay is not the original request: %d messages vs %d",
			len(provider.seen[1]), len(provider.seen[0]))
	}
	if !carriesNudge(provider.seen[2]) {
		t.Fatal("the second recovery is not the nudge")
	}
}

// TestFirstTokenGuardDoesNotReplayAfterOutput keeps the replay to the case it is
// safe in: once reasoning has reached the client, re-issuing would stream it a
// second time, so the turn must take the ordinary path instead.
func TestFirstTokenGuardDoesNotReplayAfterOutput(t *testing.T) {
	st := &session.State{
		ID:         "sess_reasoned_then_silent",
		CWD:        t.TempDir(),
		Mode:       session.ModeAgent,
		SessionDir: t.TempDir(),
	}
	provider := &emptyThenNudgedProvider{}
	ms := 100
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 20, LLMFirstTokenTimeoutMS: &ms},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }

	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "do the thing"}}); err == nil {
		t.Fatal("expected the no-reply notice")
	}
	// Every call produced reasoning, so only the empty-turn budgets applied; the
	// first-token replay must not have added calls of its own.
	if want := 1 + maxEmptyAssistantReissues + maxEmptyAssistantContinuations; provider.calls != want {
		t.Fatalf("provider called %d times, want %d", provider.calls, want)
	}
}

// TestBuildSystemPromptCustomTemplateWithoutRulesKeepsInstructions is the
// regression Codex found in review: the project AGENTS.md is dropped from
// {{.Instructions}} because the rules block carries it, so a template under
// prompts.dir that renders {{.Instructions}} and not {{.Rules}} would end up
// with neither copy.
func TestBuildSystemPromptCustomTemplateWithoutRulesKeepsInstructions(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte("PROJECT_DOC_TOKEN"), 0o644); err != nil {
		t.Fatal(err)
	}
	promptsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(promptsDir, "agent.md"), []byte("You are Coddy.\n\n{{.Instructions}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	st.ReplaceRulesCatalog(session.DiscoverRules(&config.Config{}, tmp))
	cfg := &config.Config{Paths: config.Paths{CWD: tmp}}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	cfg.Instructions.ApplyDefaults()
	cfg.Prompts.Dir = promptsDir
	a := NewAgent(cfg, st, nil, nil)

	prompt := a.buildSystemPrompt("agent", nil, nil)
	if n := strings.Count(prompt, "PROJECT_DOC_TOKEN"); n != 1 {
		t.Fatalf("a template without {{.Rules}} carries the project AGENTS.md %d time(s), want 1:\n%s", n, prompt)
	}

	// With the built-in template the rules block carries it, exactly once.
	cfg.Prompts.Dir = ""
	if n := strings.Count(a.buildSystemPrompt("agent", nil, nil), "PROJECT_DOC_TOKEN"); n != 1 {
		t.Fatalf("the built-in template carries the project AGENTS.md %d time(s), want 1", n)
	}
}

// --- turn_context.go: what travels after the history ------------------------

// The projection the loop hands to the provider is built from the working
// message slice; appending the block must never reach back into it, or the next
// tool result would land on top of a request Coddy already sent.
func TestWithTurnContextDoesNotWriteIntoTheCallersSlice(t *testing.T) {
	base := make([]llm.Message, 2, 8) // spare capacity: a naive append would stomp it
	base[0] = llm.Message{Role: llm.RoleSystem, Content: "system"}
	base[1] = llm.Message{Role: llm.RoleUser, Content: "hello"}

	sent := withTurnContext(base, "<turn_context>\nclock\n</turn_context>")
	if len(sent) != 3 || len(base) != 2 {
		t.Fatalf("lengths: sent=%d base=%d", len(sent), len(base))
	}
	grown := append(base, llm.Message{Role: llm.RoleTool, Content: "result"}) //nolint:gocritic // the point of the test
	if sent[2].Content != "<turn_context>\nclock\n</turn_context>" {
		t.Fatalf("appending to the caller's slice overwrote the sent block: %q", sent[2].Content)
	}
	if grown[2].Content != "result" {
		t.Fatalf("the caller's own append was disturbed: %q", grown[2].Content)
	}
}

func TestWithTurnContextSendsHistoryAloneWhenTheBlockIsEmpty(t *testing.T) {
	base := []llm.Message{{Role: llm.RoleSystem, Content: "system"}}
	if got := withTurnContext(base, "   "); len(got) != 1 {
		t.Fatalf("an empty block must add no message, got %d", len(got))
	}
}

func TestBuildTurnContextCarriesClockAndTodoButNoRules(t *testing.T) {
	tmp := t.TempDir()
	rulesDir := filepath.Join(tmp, ".coddy", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ndescription: Go files\nglobs: **/*.go\nalwaysApply: false\n---\n\nTURN_CTX_RULE_TOKEN\n"
	if err := os.WriteFile(filepath.Join(rulesDir, "gofiles.mdc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	st.ReplaceRulesCatalog(session.DiscoverRules(&config.Config{}, tmp))
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	a := NewAgent(cfg, st, nil, nil)
	a.clock = func() time.Time { return time.Date(2038, 1, 19, 3, 14, 7, 0, time.UTC) }

	sys := a.buildSystemPromptParts("agent", nil, nil)
	if strings.Contains(sys.Content, "TURN_CTX_RULE_TOKEN") {
		t.Fatal("a glob rule reached the system prompt before any tool touched a matching file")
	}

	block := a.buildTurnContext(sys)
	if !strings.Contains(block, "2038-01-19T03:14:07Z") {
		t.Fatalf("turn context lost the clock: %q", block)
	}
	if strings.Contains(block, "TURN_CTX_RULE_TOKEN") {
		t.Fatalf("turn context carries a rule nothing activated: %q", block)
	}

	st.SetPlan([]acp.PlanEntry{{Content: "TURN_CTX_TODO_TOKEN", Status: "pending"}})
	read := llm.ToolCall{ID: "r1", Name: "read", InputJSON: `{"path":"main.go"}`}
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{read}})
	result := toolResultMessage(read, "package main", nil, a.toolCallRules("agent", read, tmp))
	st.AddMessage(result)
	if !strings.Contains(result.Rules, "TURN_CTX_RULE_TOKEN") {
		t.Fatalf("the read's result lost the rule it activated: %+v", result)
	}

	block = a.buildTurnContext(sys)
	if !strings.Contains(block, "TURN_CTX_TODO_TOKEN") {
		t.Fatalf("turn context lost the checklist: %q", block)
	}
	// The rule rides in the result of the read, written once; repeating it in
	// the block would send it again on every step.
	if strings.Contains(block, "TURN_CTX_RULE_TOKEN") {
		t.Fatalf("turn context repeats the rule the read's result carries: %q", block)
	}
	if next := a.buildSystemPromptParts("agent", nil, nil); next.Content != sys.Content {
		t.Fatal("the activated rule rewrote the system prompt")
	}
}

// A turn renders its system prompt more than once - a rebuild after compaction,
// the continuation a permission answer starts - so reading the plan hand-off
// must not consume it. That destructive read is what used to drop the plan
// halfway through the turn that was carrying it out.
func TestSystemPromptRebuildKeepsThePlanContext(t *testing.T) {
	tmp := t.TempDir()
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	st.SetPendingPlanContext("PLAN_HANDOFF_TOKEN")
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	a := NewAgent(cfg, st, nil, nil)

	for i := 1; i <= 3; i++ {
		if got := a.buildSystemPromptParts("agent", nil, nil); !strings.Contains(got.Content, "PLAN_HANDOFF_TOKEN") {
			t.Fatalf("build %d lost the plan hand-off", i)
		}
	}

	// And it is let go when the turn ends, so the next one starts clean.
	a.releasePlanContext()
	if got := a.buildSystemPromptParts("agent", nil, nil); strings.Contains(got.Content, "PLAN_HANDOFF_TOKEN") {
		t.Fatal("the plan hand-off outlived the turn that ran the plan")
	}
}

// The hand-off is released by the turn that ran the plan, but not while a
// permission gate is still held in the bundle: what answers that gate renders
// this turn's system prompt again, possibly in another process.
func TestPlanContextSurvivesWhileAPermissionGateIsHeld(t *testing.T) {
	tmp := t.TempDir()
	store := &session.FileStore{Root: t.TempDir()}
	sd, err := store.EnsureLayout("sess_gate_hold")
	if err != nil {
		t.Fatal(err)
	}
	st := &session.State{ID: "sess_gate_hold", CWD: tmp, Mode: session.ModeAgent, SessionDir: sd}
	st.SetPendingPlanContext("PLAN_HANDOFF_TOKEN")
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	a := NewAgent(cfg, st, nil, nil)

	if err := session.WritePendingPermission(sd, acp.PermissionRequestParams{
		SessionID: "sess_gate_hold",
		ToolCall:  acp.PermissionToolCall{ToolCallID: "call_held", Title: "Run: run_command", Status: "pending"},
	}, "run_command", "{}"); err != nil {
		t.Fatal(err)
	}
	a.releasePlanContext()
	if st.PendingPlanContext() != "PLAN_HANDOFF_TOKEN" {
		t.Fatal("the hand-off was released while a permission gate was still held")
	}

	if err := session.ClearPendingPermission(sd); err != nil {
		t.Fatal(err)
	}
	a.releasePlanContext()
	if st.PendingPlanContext() != "" {
		t.Fatal("the hand-off outlived the gate that was holding it")
	}
	if session.ReadPendingPlanContext(sd) != "" {
		t.Fatal("the hand-off is still in the bundle after the turn ended")
	}
}

// The built-in plan and ask templates never showed the session checklist, and
// neither mode offers the todo tools. Moving the block into the turn context
// must not start showing it there.
func TestTurnContextCarriesTheChecklistInAgentModeOnly(t *testing.T) {
	tmp := t.TempDir()
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	st.SetPlan([]acp.PlanEntry{{Content: "MODE_TODO_TOKEN", Status: "pending"}})
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	a := NewAgent(cfg, st, nil, nil)

	for mode, want := range map[string]bool{"agent": true, "plan": false, "ask": false} {
		sys := a.buildSystemPromptParts(mode, nil, nil)
		block := a.buildTurnContext(sys)
		if got := strings.Contains(block, "MODE_TODO_TOKEN"); got != want {
			t.Errorf("%s mode: checklist in the turn context = %v, want %v", mode, got, want)
		}
		if strings.Contains(sys.Content, "MODE_TODO_TOKEN") {
			t.Errorf("%s mode: the checklist reached the frozen system prompt", mode)
		}
	}
}

// A template under prompts.dir that prints {{.UTCNow}} or {{.TodoList}} keeps
// the pre-cache behaviour: re-rendered before every call, and no turn context
// block, so its own conditionals around those fields stay true and the model is
// not handed two clocks.
func TestVolatileCustomTemplateKeepsThePerStepRefresh(t *testing.T) {
	tmp := t.TempDir()
	promptsDir := t.TempDir()
	body := "You are Coddy.\n\n{{if .TodoList}}## Checklist\n\n{{.TodoList}}\n{{end}}\nNow: {{.UTCNow}}\n"
	if err := os.WriteFile(filepath.Join(promptsDir, "agent.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{CWD: tmp}}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	cfg.Prompts.Dir = promptsDir
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	a := NewAgent(cfg, st, nil, nil)

	sys := a.buildSystemPromptParts("agent", nil, nil)
	if !sys.Volatile {
		t.Fatal("a template printing UTCNow and TodoList must be marked volatile")
	}
	if block := a.buildTurnContext(sys); block != "" {
		t.Fatalf("a volatile template must get no turn context block, got %q", block)
	}

	// The built-in template is the other way round.
	cfg.Prompts.Dir = ""
	builtin := a.buildSystemPromptParts("agent", nil, nil)
	if builtin.Volatile {
		t.Fatal("the built-in agent template must not be volatile")
	}
	if block := a.buildTurnContext(builtin); !strings.Contains(block, turnContextOpenTag) {
		t.Fatalf("the built-in template must get a turn context block, got %q", block)
	}
}

// A template with no {{.Rules}} in it asked for no rules at all. A rule a tool
// call activates must not be smuggled in after the history either.
func TestTemplateWithoutRulesGetsNoRulesAfterTheHistory(t *testing.T) {
	tmp := t.TempDir()
	rulesDir := filepath.Join(tmp, ".coddy", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ndescription: Go files\nglobs: **/*.go\nalwaysApply: false\n---\n\nNO_RULES_TEMPLATE_TOKEN\n"
	if err := os.WriteFile(filepath.Join(rulesDir, "gofiles.mdc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	promptsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(promptsDir, "agent.md"), []byte("You are Coddy. {{.CWD}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{CWD: tmp}}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	cfg.Prompts.Dir = promptsDir
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	st.ReplaceRulesCatalog(session.DiscoverRules(cfg, tmp))
	a := NewAgent(cfg, st, nil, nil)

	sys := a.buildSystemPromptParts("agent", nil, nil)
	read := llm.ToolCall{ID: "r1", Name: "read", InputJSON: `{"path":"main.go"}`}
	if res := toolResultMessage(read, "package main", nil, a.toolCallRules("agent", read, tmp)); res.Rules != "" {
		t.Fatalf("a template without {{.Rules}} still received a rule with a tool result: %q", res.Rules)
	}
	if block := a.buildTurnContext(sys); strings.Contains(block, "NO_RULES_TEMPLATE_TOKEN") {
		t.Fatalf("a template without {{.Rules}} still received a rule: %q", block)
	}
}

// A glob rule an attached file brought in rides in the user's message, never
// in the system prompt, and is not repeated with a later tool result: that
// would spend exactly the tokens the attachment already paid.
func TestRuleAnAttachmentBroughtIsNotRepeatedWithAToolResult(t *testing.T) {
	tmp := t.TempDir()
	rulesDir := filepath.Join(tmp, ".coddy", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ndescription: Go files\nglobs: **/*.go\nalwaysApply: true\n---\n\nALREADY_SENT_RULE_TOKEN\n"
	if err := os.WriteFile(filepath.Join(rulesDir, "gofiles.mdc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main.go", "other.go"} {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Paths: config.Paths{CWD: tmp}}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	st.ReplaceRulesCatalog(session.DiscoverRules(cfg, tmp))
	a := NewAgent(cfg, st, nil, nil)

	prompt := a.attachActivatedRules([]acp.ContentBlock{
		{Type: acp.ContentTypeText, Text: "look"},
		{Type: acp.ContentTypeResource, Resource: &acp.Resource{URI: "file://" + filepath.ToSlash(filepath.Join(tmp, "main.go"))}},
	})
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: contentBlocksToText(prompt)})
	if !strings.Contains(st.GetMessages()[0].Content, "ALREADY_SENT_RULE_TOKEN") {
		t.Fatal("the attached file did not bring the glob rule into its message")
	}
	sys := a.buildSystemPromptParts("agent", nil, nil)
	if strings.Contains(sys.Content, "ALREADY_SENT_RULE_TOKEN") {
		t.Fatal("a glob rule reached the system prompt")
	}
	read := llm.ToolCall{ID: "r1", Name: "read", InputJSON: `{"path":"other.go"}`}
	if res := toolResultMessage(read, "package main", nil, a.toolCallRules("agent", read, tmp)); strings.Contains(res.Rules, "ALREADY_SENT_RULE_TOKEN") {
		t.Fatalf("a rule the user's message carries was repeated with a tool result: %q", res.Rules)
	}
	if block := a.buildTurnContext(sys); strings.Contains(block, "ALREADY_SENT_RULE_TOKEN") {
		t.Fatalf("a rule the user's message carries was repeated after the history: %q", block)
	}
}

// The lane re-issues a step that produced nothing, and that replay must be the
// request that failed. A clock ticking between the two would make it a
// different request and miss the cache the first attempt just populated.
func TestTurnClockDoesNotTickBetweenTheStepsOfATurn(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	a := NewAgent(cfg, st, nil, nil)

	ticks := 0
	a.clock = func() time.Time {
		ticks++
		return time.Date(2038, 1, 19, 3, 14, 7+ticks, 0, time.UTC)
	}

	sys := a.buildSystemPromptParts("agent", nil, nil)
	first := a.buildTurnContext(sys)
	second := a.buildTurnContext(sys)
	if first != second {
		t.Fatalf("the turn context clock moved between two steps of one turn:\n%q\n%q", first, second)
	}
	if !strings.Contains(first, "2038-01-19T03:14:08Z") {
		t.Fatalf("the block does not carry the turn's own stamp: %q", first)
	}
}

// --- Session filing (session_describe) -------------------------------------

func TestApplySessionFilingRefusesATitleTooLongForARowAndWritesNothing(t *testing.T) {
	st := &session.State{ID: "sess_filing", CWD: t.TempDir(), Mode: session.ModeAgent}
	st.SetTitlePinned("A short title")
	st.SetTags([]string{"api"})

	long := strings.Repeat("x", session.MaxSessionTitleRunes+1)
	tags := []string{"backend"}
	if _, err := applySessionFiling(st, tooling.SessionFilingUpdate{Title: &long, Tags: &tags}); err == nil {
		t.Fatal("a title longer than a list row was accepted")
	}
	if got := st.ConversationTitle(); got != "A short title" {
		t.Fatalf("the refused call still renamed the session to %q", got)
	}
	if got := st.GetTags(); !reflect.DeepEqual(got, []string{"api"}) {
		t.Fatalf("the refused call still filed the session under %v", got)
	}
}

func TestApplySessionFilingClearsThePinOnAnEmptyTitle(t *testing.T) {
	st := &session.State{ID: "sess_filing", CWD: t.TempDir(), Mode: session.ModeAgent}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "fix the failing test"})
	st.SetTitlePinned("Pinned by the model")

	empty := "   "
	filing, err := applySessionFiling(st, tooling.SessionFilingUpdate{Title: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if filing.Filing.Title == "Pinned by the model" || filing.Filing.Title == "" {
		t.Fatalf("clearing the pin left the title %q, want the one derived from the first message", filing.Filing.Title)
	}
}

func TestApplySessionFilingEditsTheTagsInPlace(t *testing.T) {
	st := &session.State{ID: "sess_filing", CWD: t.TempDir(), Mode: session.ModeAgent}
	st.SetTags([]string{"api", "backend"})

	filing, err := applySessionFiling(st, tooling.SessionFilingUpdate{
		AddTags:    []string{"Session Store"},
		RemoveTags: []string{"API"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(filing.Filing.Tags, []string{"backend", "session-store"}) {
		t.Fatalf("got %v", filing.Filing.Tags)
	}
}

func TestApplySessionFilingClearsTheTagsOnAnEmptyList(t *testing.T) {
	st := &session.State{ID: "sess_filing", CWD: t.TempDir(), Mode: session.ModeAgent}
	st.SetTags([]string{"api"})

	none := []string{}
	filing, err := applySessionFiling(st, tooling.SessionFilingUpdate{Tags: &none})
	if err != nil {
		t.Fatal(err)
	}
	if len(filing.Filing.Tags) != 0 {
		t.Fatalf("got %v, want no tags", filing.Filing.Tags)
	}
}

func TestSessionDescribeIsOfferedToAPlannerAndNotToAsk(t *testing.T) {
	// Filing writes the session's own title and tags and nothing else, so a
	// planning session may do it; ask mode stays read-only.
	if !ToolSetForMode("plan").Allows(tools.ToolSessionDescribe) {
		t.Fatal("plan mode cannot file its own session")
	}
	if ToolSetForMode("ask").Allows(tools.ToolSessionDescribe) {
		t.Fatal("ask mode was offered a tool that writes")
	}
	if !ToolSetForMode("agent").Unrestricted() {
		t.Fatal("agent mode is no longer unrestricted")
	}
}

func TestApplySessionFilingReportsAClearedPinBehindTheSameWords(t *testing.T) {
	// The pinned title and the derived one can read alike; clearing the pin is
	// still a change, and a report built by comparing the effective title
	// before and after would call it nothing.
	st := &session.State{ID: "sess_filing", CWD: t.TempDir(), Mode: session.ModeAgent}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "Fix the failing test"})
	derived := st.ConversationTitle()
	st.SetTitlePinned(derived)

	empty := ""
	result, err := applySessionFiling(st, tooling.SessionFilingUpdate{Title: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Changed, []string{"title"}) {
		t.Fatalf("changed = %v, want the title", result.Changed)
	}
	if st.GetTitlePinned() != "" {
		t.Fatalf("the pin survived: %q", st.GetTitlePinned())
	}
}

func TestApplySessionFilingReportsNothingWhenTheCallNamesNothing(t *testing.T) {
	st := &session.State{ID: "sess_filing", CWD: t.TempDir(), Mode: session.ModeAgent}
	st.SetTitlePinned("A title")
	st.SetTags([]string{"api"})

	result, err := applySessionFiling(st, tooling.SessionFilingUpdate{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changed) != 0 {
		t.Fatalf("a read reported %v as changed", result.Changed)
	}
	if result.Filing.Title != "A title" || !reflect.DeepEqual(result.Filing.Tags, []string{"api"}) {
		t.Fatalf("a read reported %+v", result.Filing)
	}
}

func TestUpdateTagsKeepsWhatAnotherWriterFiledMeanwhile(t *testing.T) {
	// The point of add_tags is "keep the rest". Merging outside the session
	// would drop whatever another surface filed between the read and the write,
	// so the merge happens under the session's own lock - which is what makes
	// eight concurrent additions end up with eight labels.
	st := &session.State{ID: "sess_filing", CWD: t.TempDir(), Mode: session.ModeAgent}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			st.UpdateTags([]string{fmt.Sprintf("tag-%d", n)}, nil)
		}(i)
	}
	wg.Wait()
	if got := st.GetTags(); len(got) != 8 {
		t.Fatalf("concurrent additions left %v", got)
	}
}

func TestSetTitlePinnedIfUnsetHasOneWinner(t *testing.T) {
	st := &session.State{ID: "sess_filing", CWD: t.TempDir(), Mode: session.ModeAgent}
	var wg sync.WaitGroup
	var wins atomic.Int64
	for i := range 8 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if _, written := st.SetTitlePinnedIfUnset(fmt.Sprintf("name %d", n)); written {
				wins.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d callers named the session", wins.Load())
	}
	if st.GetTitlePinned() == "" {
		t.Fatal("nobody named it")
	}
}

// --- stalled streams and interrupted waits ---------------------------------

// A stream that goes quiet after its first deltas is cut by the idle guard:
// the text the user already watched stream in is persisted, the step runs
// again after a pause like any failure of the provider (issue #246), and a
// lane that stalls every time ends the turn with the stall named, not with a
// silent wait for a byte that never comes.
func TestStalledStreamKeepsPartialAnswerAndReportsTheStall(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"content\":\"Hello fr\"}}],\"id\":\"c1\",\"model\":\"m\",\"object\":\"chat.completion.chunk\"}\n\n")
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	idle := 200
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "stub", Type: "openai", APIKey: "test", APIBase: srv.URL}},
		Models:    []config.ModelEntry{{Model: "stub/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "stub/model", MaxTurns: 5, LLMStreamIdleTimeoutMS: &idle, LLMRetryBaseMS: 1},
	}
	st := &session.State{ID: "sess_stall", CWD: t.TempDir(), Mode: session.ModeAgent, SessionDir: t.TempDir()}
	sender := &loopGuardSender{}
	ag := NewAgent(cfg, st, sender, nil)

	// Bounded so a guard that never fires fails the test instead of hanging it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "hello"}})
	if err == nil || !llm.IsStreamStalled(err) {
		t.Fatalf("the turn must end with the stall, got err=%v", err)
	}
	if !strings.Contains(err.Error(), "200ms") {
		t.Fatalf("the error must name the idle time: %v", err)
	}
	msgs := st.GetMessages()
	if len(msgs) == 0 {
		t.Fatal("no messages persisted")
	}
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant || last.Content != "Hello fr" {
		t.Fatalf("the partial answer must be persisted, last message = %+v", last)
	}
	if got := calls.Load(); got != 1+maxProviderRecoveries {
		t.Fatalf("the stalling lane was called %d times, want the call and %d recoveries", got, maxProviderRecoveries)
	}
}

// silentStreamProvider never sends a chunk and returns only when the caller's
// context ends, as a provider does when the upstream holds the request.
type silentStreamProvider struct{}

func (silentStreamProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("Complete must not be used here")
}

func (silentStreamProvider) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// A turn interrupted from outside (a signal, a shutdown) while the model has
// produced nothing says how long the model had been silent, so a kill after
// a long silent wait is not reported as a mere interruption.
func TestInterruptedBeforeOutputNamesTheSilence(t *testing.T) {
	guard := 60000
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "lane", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "lane/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "lane/model", MaxTurns: 3, LLMFirstTokenTimeoutMS: &guard},
	}
	st := &session.State{ID: "sess_silent", CWD: t.TempDir(), Mode: session.ModeAgent, SessionDir: t.TempDir()}
	ag := NewAgent(cfg, st, &loopGuardSender{}, nil)
	ag.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return silentStreamProvider{}, nil })

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	_, err := ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "hello"}})
	if err == nil || !strings.Contains(err.Error(), "interrupted before producing a response") {
		t.Fatalf("expected the interruption error, got %v", err)
	}
	if !strings.Contains(err.Error(), "silent for") {
		t.Fatalf("the error must say how long the model was silent: %v", err)
	}
}

// sessionBypassSender answers the first permission prompt with "bypass
// permissions for this session" and records every prompt it is shown.
type sessionBypassSender struct {
	mu       sync.Mutex
	requests []acp.PermissionRequestParams
}

func (s *sessionBypassSender) SendSessionUpdate(string, interface{}) error { return nil }

func (s *sessionBypassSender) RequestPermission(_ context.Context, p acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, p)
	return &acp.PermissionResult{Outcome: "selected", OptionID: permission.OptionAllowSessionBypass}, nil
}

func (s *sessionBypassSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

// TestSessionBypassFromTheDialogCoversTheRestOfTheBatch pins #292: choosing
// "bypass for this session" approves the call and switches the
// session, so the next call of the same batch runs without a prompt.
func TestSessionBypassFromTheDialogCoversTheRestOfTheBatch(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	cfg.Tools.PermissionMode = config.PermModeAsk
	st := &session.State{ID: "sess_dialog_bypass", CWD: dir, Mode: session.ModeAgent}
	sender := &sessionBypassSender{}
	provider := &scriptedProvider{steps: []scriptStep{
		toolStep(
			llm.ToolCall{ID: "c1", Name: "run_command", InputJSON: `{"command":"echo first"}`},
			llm.ToolCall{ID: "c2", Name: "run_command", InputJSON: `{"command":"echo second"}`},
		),
		answerStep("done"),
	}}
	ag := NewAgent(cfg, st, sender, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "run both"}}); err != nil {
		t.Fatal(err)
	}
	if len(sender.requests) != 1 {
		t.Fatalf("prompts = %d, want only the first call's", len(sender.requests))
	}
	var offered []string
	for _, o := range sender.requests[0].Options {
		offered = append(offered, o.OptionID)
	}
	if !strings.Contains(strings.Join(offered, ","), permission.OptionAllowSessionBypass) {
		t.Fatalf("the prompt did not offer the session switch: %v", offered)
	}
	if sender.requests[0].SessionPermissionMode != config.PermModeAsk {
		t.Fatalf("the prompt was stamped %q, want ask", sender.requests[0].SessionPermissionMode)
	}
	if got := st.GetPermissionMode(); got != config.PermModeBypass {
		t.Fatalf("session permission mode = %q, want bypass", got)
	}
	var results int
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleTool && (strings.Contains(m.Content, "first") || strings.Contains(m.Content, "second")) {
			results++
		}
	}
	if results != 2 {
		t.Fatalf("tool results that ran = %d, want both calls", results)
	}
}

// settingsHarness runs turns through a real session manager, with one scripted
// provider per configured model, so a test sees which model served a request.
type settingsHarness struct {
	mgr       *session.Manager
	sessionID string
	mu        sync.Mutex
	providers map[string]*scriptedProvider
	// onBuild, when set, runs whenever the agent builds a provider for an
	// API model id, before the provider is handed back.
	onBuild func(apiModel string)
}

func newSettingsHarness(t *testing.T, models ...string) *settingsHarness {
	t.Helper()
	return newSettingsHarnessWith(t, nil, models...)
}

// newSettingsHarnessWith is newSettingsHarness with tune applied to the
// configuration before the manager sees it.
func newSettingsHarnessWith(t *testing.T, tune func(*config.Config), models ...string) *settingsHarness {
	t.Helper()
	root := t.TempDir()
	cwd := t.TempDir()
	cfg := &config.Config{
		Paths:     config.Paths{Home: root, CWD: cwd, ConfigPath: filepath.Join(root, "config.yaml")},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Agent:     config.Agent{Model: models[0], MaxTurns: 8},
		Sessions:  config.Sessions{Dir: filepath.Join(root, "sessions")},
	}
	for _, m := range models {
		cfg.Models = append(cfg.Models, config.ModelEntry{Model: m, MaxTokens: 100})
	}
	if tune != nil {
		tune(cfg)
	}
	cfg.Tools.PermissionMode = config.PermModeBypass
	cfg.Subagents.ApplyDefaults(cfg.Paths)
	cfg.Hooks.ApplyDefaults(cfg.Paths)
	cfg.Prompts.ApplyDefaults()
	h := &settingsHarness{providers: map[string]*scriptedProvider{}}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		loop := NewAgent(cfg, st, snd, slog.Default())
		loop.SetSubagentRuntime(h.mgr)
		loop.SetProviderFactory(func(in llm.ProviderInput) (llm.Provider, error) {
			h.mu.Lock()
			onBuild := h.onBuild
			h.mu.Unlock()
			if onBuild != nil {
				onBuild(in.Model)
			}
			return h.provider(in.Model), nil
		})
		return loop.Run(ctx, prompt)
	}
	// Sessions persist, because a subagent's bundle nests in its parent's.
	h.mgr = session.NewManager(cfg, &todoSnapshotSender{}, runner, slog.Default(), cwd, &session.FileStore{Root: cfg.Sessions.Dir})
	res, err := h.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	h.sessionID = res.SessionID
	return h
}

// provider returns the scripted provider of an API model id ("a" for fake/a).
func (h *settingsHarness) provider(apiModel string) *scriptedProvider {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.providers[apiModel]
	if !ok {
		p = &scriptedProvider{}
		h.providers[apiModel] = p
	}
	return p
}

func (h *settingsHarness) prompt(t *testing.T, text string) {
	t.Helper()
	if _, err := h.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: h.sessionID,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSwitchModelTakesEffectFromTheNextRequest(t *testing.T) {
	h := newSettingsHarness(t, "fake/a", "fake/b")
	h.provider("a").steps = []scriptStep{
		toolStep(llm.ToolCall{ID: "sw1", Name: "switch_model", InputJSON: `{"model":"fake/b","scope":"turn"}`}),
	}
	h.provider("b").steps = []scriptStep{answerStep("done on b")}
	h.prompt(t, "use fake/b for this turn only")
	if a, b := h.provider("a").calls, h.provider("b").calls; a != 1 || b != 1 {
		t.Fatalf("requests served: a=%d b=%d, want the first on a and the next on b", a, b)
	}
	st := h.mgr.SessionByID(h.sessionID)
	if st.GetSelectedModelID() != "" {
		t.Fatalf("a turn-scoped switch changed the session model to %q", st.GetSelectedModelID())
	}
	// The next turn is back on the session's model.
	h.provider("a").steps = append(h.provider("a").steps, answerStep("back on a"))
	h.prompt(t, "and now")
	if a := h.provider("a").calls; a != 2 {
		t.Fatalf("the next turn was not served by the session's model: a=%d", a)
	}
}

// switchModelDuring answers like a model a browser interrupts with
// PATCH /coddy/sessions/{id}: part of the answer streams, the session's model
// is switched to model, and the answer ends with a tool call, so the turn
// takes another step.
func switchModelDuring(t *testing.T, h *settingsHarness, model, text string) scriptStep {
	return func(_ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response {
		onChunk(llm.StreamChunk{TextDelta: text})
		to := model
		if _, err := h.mgr.ApplySessionSettings(context.Background(), h.sessionID, session.SettingsChange{Model: &to, Source: "web"}); err != nil {
			t.Errorf("switch the model: %v", err)
		}
		call := llm.ToolCall{ID: "g1", Name: "glob", InputJSON: `{"pattern":"*.none"}`}
		onChunk(llm.StreamChunk{ToolCall: &call})
		return &llm.Response{Content: text, ToolCalls: []llm.ToolCall{call}, StopReason: "tool_use"}
	}
}

// seedExchanges gives the session earlier turns, so a compaction has
// something to fold.
func (h *settingsHarness) seedExchanges(n int, model string) {
	st := h.mgr.SessionByID(h.sessionID)
	for i := 0; i < n; i++ {
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("earlier question %d", i+1)})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("earlier answer %d", i+1), Model: model})
	}
}

func (h *settingsHarness) compacted() bool {
	for _, m := range h.mgr.SessionByID(h.sessionID).GetMessages() {
		if m.CompactionSummary {
			return true
		}
	}
	return false
}

// A model switched while an answer streams takes the turn's next request, and
// the answer in flight keeps the name of the model that wrote it (#362).
func TestAnswerIsSignedByTheModelThatWroteIt(t *testing.T) {
	h := newSettingsHarness(t, "fake/a", "fake/b")
	h.provider("a").steps = []scriptStep{switchModelDuring(t, h, "fake/b", "written by a")}
	h.provider("b").steps = []scriptStep{answerStep("written by b")}
	h.prompt(t, "go")
	if a, b := h.provider("a").calls, h.provider("b").calls; a != 1 || b != 1 {
		t.Fatalf("requests served: a=%d b=%d, want the step after the switch on b", a, b)
	}
	var signed []string
	for _, m := range h.mgr.SessionByID(h.sessionID).GetMessages() {
		if m.Role == llm.RoleAssistant {
			signed = append(signed, m.Content+" | "+m.Model)
		}
	}
	if want := []string{"written by a | fake/a", "written by b | fake/b"}; !reflect.DeepEqual(signed, want) {
		t.Fatalf("assistant rows %q, want %q", signed, want)
	}
}

// A switch that lands after the turn built its transport and before its first
// request - here while the turn compacts first - reaches that request: the
// loop compares the settings with what the transport was built for, not with
// what it found when it started.
func TestSwitchBeforeTheFirstRequestReachesIt(t *testing.T) {
	keep := 1
	h := newSettingsHarnessWith(t, func(cfg *config.Config) {
		// The system prompt alone crosses 80% of this window, so the turn
		// compacts before its first request.
		cfg.Models[0].MaxContextTokens = 50
		cfg.Compaction.KeepRecentTurns = &keep
	}, "fake/a", "fake/b")
	h.seedExchanges(3, "fake/a")
	b := "fake/b"
	h.provider("a").steps = []scriptStep{func(_ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) *llm.Response {
		// The summarizer's request: the operator switches meanwhile.
		if _, err := h.mgr.ApplySessionSettings(context.Background(), h.sessionID, session.SettingsChange{Model: &b, Source: "web"}); err != nil {
			t.Errorf("switch the model: %v", err)
		}
		return &llm.Response{Content: "summary of the earlier turns", StopReason: "end_turn"}
	}}
	h.provider("b").steps = []scriptStep{answerStep("written by b")}
	h.prompt(t, "go")
	if !h.compacted() {
		t.Fatal("the turn did not compact before its first request")
	}
	if a, bCalls := h.provider("a").calls, h.provider("b").calls; a != 1 || bCalls != 1 {
		t.Fatalf("requests served: a=%d b=%d, want the summary on a and the first request on b", a, bCalls)
	}
}

// A second switch that lands while the loop builds the transport for the
// first is not left for the step after: the request goes to the model the
// session names when it is sent.
func TestSwitchDuringTheRebuildReachesTheRequest(t *testing.T) {
	h := newSettingsHarness(t, "fake/a", "fake/b", "fake/c")
	var once sync.Once
	h.onBuild = func(apiModel string) {
		if apiModel != "b" {
			return
		}
		once.Do(func() {
			c := "fake/c"
			if _, err := h.mgr.ApplySessionSettings(context.Background(), h.sessionID, session.SettingsChange{Model: &c, Source: "web"}); err != nil {
				t.Errorf("switch the model: %v", err)
			}
		})
	}
	h.provider("a").steps = []scriptStep{switchModelDuring(t, h, "fake/b", "written by a")}
	h.provider("c").steps = []scriptStep{answerStep("written by c")}
	h.prompt(t, "go")
	if a, b, c := h.provider("a").calls, h.provider("b").calls, h.provider("c").calls; a != 1 || b != 0 || c != 1 {
		t.Fatalf("requests served: a=%d b=%d c=%d, want the step after the switches on c", a, b, c)
	}
}

// A switch to a model whose window only its provider's listing reports reads
// that listing before the next request and compacts against it, in the middle
// of a turn and between turns alike, instead of measuring against the 128000
// default (#362).
func TestSwitchToASmallerWindowCompactsBeforeTheNextRequest(t *testing.T) {
	for _, tc := range []struct {
		name     string
		midTurn  bool
		wantACnt int
	}{
		{name: "in the middle of a turn", midTurn: true, wantACnt: 1},
		{name: "between turns", wantACnt: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSettingsHarnessWith(t, func(cfg *config.Config) {
				// fake/b reports its window through the provider's listing
				// only; fake/a declares a large one of its own.
				cfg.Providers[0].APIBase = "https://listing.invalid/v1"
				cfg.Models[0].MaxContextTokens = 1_000_000
			}, "fake/a", "fake/b")
			var listings atomic.Int32
			h.mgr.SetContextWindowLister(func(context.Context, llm.ProviderInput) ([]llm.ModelEntry, error) {
				listings.Add(1)
				return []llm.ModelEntry{{ID: "b", ContextWindow: 50}}, nil
			}, nil)
			t.Cleanup(func() { _ = h.mgr.WaitContextWindowsIdle(5 * time.Second) })
			h.seedExchanges(4, "fake/a")
			if tc.midTurn {
				h.provider("a").steps = []scriptStep{switchModelDuring(t, h, "fake/b", "written by a")}
			} else {
				b := "fake/b"
				if _, err := h.mgr.ApplySessionSettings(context.Background(), h.sessionID, session.SettingsChange{Model: &b, Source: "web"}); err != nil {
					t.Fatal(err)
				}
			}
			h.provider("b").steps = []scriptStep{answerStep("summary of the earlier turns"), answerStep("written by b")}
			h.prompt(t, "go")
			if !h.compacted() {
				w, source := h.mgr.SessionByID(h.sessionID).ContextWindow(h.mgr.Cfg())
				t.Fatalf("no compaction before the request to fake/b; the session measures against %d (%s), listings read: %d", w, source, listings.Load())
			}
			if a := h.provider("a").calls; a != tc.wantACnt {
				t.Fatalf("fake/a served %d requests, want %d", a, tc.wantACnt)
			}
		})
	}
}

// The system prompt describes switch_model only to a turn that is offered it:
// a parent with two models reads the rule, a subagent - never offered the
// tool - and a configuration with nothing to switch do not.
func TestSystemPromptDescribesSwitchModelOnlyWhereItIsOffered(t *testing.T) {
	const rule = "Use `switch_model`"
	h := newSettingsHarness(t, "fake/a", "fake/b")
	h.provider("a").steps = []scriptStep{
		toolStep(llm.ToolCall{ID: "sp", Name: "spawn_agent", InputJSON: `{"agent":"general","prompt":"Report the working directory."}`}),
		answerStep("REPORT: done"),
		answerStep("done"),
	}
	h.prompt(t, "delegate it")
	reqs := h.provider("a").requests
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want the parent's, the child's and the parent's again", len(reqs))
	}
	if !strings.Contains(reqs[0][0].Content, rule) {
		t.Error("the parent's system prompt does not describe switch_model")
	}
	if strings.Contains(reqs[1][0].Content, rule) {
		t.Error("the subagent's system prompt describes switch_model, which it is never offered")
	}
	// Nothing wakes a subagent either: its transcript is sealed when its
	// turn returns, so its prompt tells it to collect results itself.
	if !strings.Contains(reqs[1][0].Content, "Nothing wakes you here") {
		t.Error("the subagent's system prompt promises a background wake it can never get")
	}

	single := newSettingsHarness(t, "fake/a")
	single.provider("a").steps = []scriptStep{answerStep("hi")}
	single.prompt(t, "hello")
	if strings.Contains(single.provider("a").requests[0][0].Content, rule) {
		t.Error("a single model without reasoning levels is told about switch_model")
	}
}

func TestSkillFrontmatterRunsTheTurnOnItsModel(t *testing.T) {
	h := newSettingsHarness(t, "fake/a", "fake/b")
	st := h.mgr.SessionByID(h.sessionID)
	st.ReplaceSkills([]*skills.Skill{{Name: "heavy", FilePath: "/skills/heavy/SKILL.md", Description: "d", Content: "Think hard.", Model: "fake/b"}})
	h.provider("b").steps = []scriptStep{answerStep("done on b")}
	h.prompt(t, "/heavy review this")
	if a, b := h.provider("a").calls, h.provider("b").calls; a != 0 || b != 1 {
		t.Fatalf("requests served: a=%d b=%d, want the whole turn on the skill's model", a, b)
	}
}

func TestSpawnAgentRefusesAModelTheConfigDoesNotKnow(t *testing.T) {
	h := newSettingsHarness(t, "fake/a")
	h.provider("a").steps = []scriptStep{
		toolStep(llm.ToolCall{ID: "sp1", Name: "spawn_agent", InputJSON: `{"agent":"general","prompt":"look around","model":"fake/nope"}`}),
		answerStep("ok"),
	}
	h.prompt(t, "delegate")
	var result string
	for _, m := range h.mgr.SessionByID(h.sessionID).GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == "sp1" {
			result = m.Content
		}
	}
	if !strings.Contains(result, `unknown model "fake/nope"`) {
		t.Fatalf("spawn_agent result = %q, want the unknown model named", result)
	}
}
