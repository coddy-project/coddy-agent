package agent

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/llm/acptest"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// TestHelperACPAgent turns the test binary into the scripted ACP agent when
// acptest.Command started it; the ordinary run skips it.
func TestHelperACPAgent(t *testing.T) {
	if !acptest.Main(flag.Args()) {
		t.Skip("helper process")
	}
}

// acpSender records what the loop sends the surface and answers permission
// prompts with answer.
type acpSender struct {
	answer *acp.PermissionResult
	// onText, when set, runs at the first answer text that reaches the
	// surface (a test stopping the turn there).
	onText func()

	mu      sync.Mutex
	updates []interface{}
	asked   []acp.PermissionRequestParams
}

func (s *acpSender) SendSessionUpdate(_ string, update interface{}) error {
	s.mu.Lock()
	s.updates = append(s.updates, update)
	stop := s.onText
	if c, ok := update.(acp.MessageChunkUpdate); ok && c.SessionUpdate == acp.UpdateTypeAgentMessageChunk && c.Content.Type == acp.ContentTypeText && stop != nil {
		s.onText = nil
	} else {
		stop = nil
	}
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
	return nil
}

func (s *acpSender) RequestPermission(_ context.Context, p acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, p)
	if s.answer == nil {
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	}
	return s.answer, nil
}

func (s *acpSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

// acpLoopConfig is a configuration whose only model is served by the fake
// agent running script.
func acpLoopConfig(t *testing.T, script acptest.Script) (*config.Config, string) {
	t.Helper()
	if script.Log == "" {
		script.Log = t.TempDir() + "/acp.log"
	}
	t.Setenv(llm.ACPDepthEnv, "")
	cmd, args := acptest.Command(t, script)
	t.Cleanup(llm.StopACPAgents)
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "agent", Type: "acp", ACP: &config.ACPProviderConfig{Command: cmd, Args: args}}},
		Models:    []config.ModelEntry{{Model: "agent/default", MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "agent/default"},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	return cfg, script.Log
}

func runACPTurn(t *testing.T, cfg *config.Config, st *session.State, sender acp.UpdateSender, text string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := NewAgent(cfg, st, sender, nil).Run(ctx, []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}})
	return err
}

func acpPrompts(t *testing.T, logPath string) []string {
	t.Helper()
	var out []string
	for _, r := range acptest.Methods(acptest.ReadLog(t, logPath), "session/prompt") {
		var p struct {
			Prompt []struct {
				Text string `json:"text"`
			} `json:"prompt"`
		}
		if err := json.Unmarshal(r.Params, &p); err != nil {
			t.Fatal(err)
		}
		var texts []string
		for _, b := range p.Prompt {
			texts = append(texts, b.Text)
		}
		out = append(out, strings.Join(texts, "\n---\n"))
	}
	return out
}

func acpLastAnswer(st *session.State) string {
	msgs := st.GetMessages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant {
			return msgs[i].Content
		}
	}
	return ""
}

// Two turns of a session go to one ACP session, the second carrying only the
// new message (and the turn's runtime block).
func TestACPModelKeepsOneAgentSessionAcrossTurns(t *testing.T) {
	cfg, logPath := acpLoopConfig(t, acptest.Script{})
	st := &session.State{ID: "sess_acp", CWD: t.TempDir(), Mode: session.ModeAgent}
	sender := &acpSender{}
	if err := runACPTurn(t, cfg, st, sender, "first question"); err != nil {
		t.Fatal(err)
	}
	if got := acpLastAnswer(st); !strings.HasPrefix(got, "echo: first question") {
		t.Fatalf("answer = %q", got)
	}
	if err := runACPTurn(t, cfg, st, sender, "second question"); err != nil {
		t.Fatal(err)
	}
	log := acptest.ReadLog(t, logPath)
	if n := len(acptest.Methods(log, "session/new")); n != 1 {
		t.Fatalf("session/new = %d, want one session for the conversation", n)
	}
	prompts := acpPrompts(t, logPath)
	if len(prompts) != 2 {
		t.Fatalf("prompts = %d", len(prompts))
	}
	if !strings.HasPrefix(prompts[1], "second question") || strings.Contains(prompts[1], "first question") {
		t.Fatalf("second prompt = %q, want only the new message", prompts[1])
	}
	if strings.Contains(prompts[0], "You are") || strings.Contains(prompts[0], "## Tools") {
		t.Fatalf("the system prompt reached the agent: %q", prompts[0])
	}
}

// A prompt the agent ends with tool work and no text is answered with a line
// of its own, and the loop does not send the agent the same prompt again.
func TestACPModelEmptyAnswerIsNotReissued(t *testing.T) {
	cfg, logPath := acpLoopConfig(t, acptest.Script{Turns: []acptest.Turn{{Tools: []acptest.Tool{{ID: "t1", Title: "Edit main.go"}}}}})
	st := &session.State{ID: "sess_acp", CWD: t.TempDir(), Mode: session.ModeAgent}
	if err := runACPTurn(t, cfg, st, &acpSender{}, "fix it"); err != nil {
		t.Fatal(err)
	}
	if got := acpLastAnswer(st); got != "(the agent finished without a message)" {
		t.Fatalf("answer = %q", got)
	}
	if n := len(acpPrompts(t, logPath)); n != 1 {
		t.Fatalf("prompts = %d, want the prompt sent once", n)
	}
}

// The agent's permission request reaches the surface as a prompt of Coddy's
// own vocabulary, stamped with the session's mode and shown as a tool row,
// and the answer reaches the agent as its own option.
func TestACPModelForwardsTheAgentsPermissionRequest(t *testing.T) {
	cfg, _ := acpLoopConfig(t, acptest.Script{Turns: []acptest.Turn{{Permission: &acptest.Permission{
		Title: "Write a.txt", Kind: "edit", Content: "hello",
		Options: []acptest.Option{
			{ID: "yes", Name: "Allow once", Kind: "allow_once"},
			{ID: "no", Name: "Reject", Kind: "reject_once"},
		},
	}}}})
	st := &session.State{ID: "sess_acp", CWD: t.TempDir(), Mode: session.ModeAgent}
	sender := &acpSender{answer: &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}}
	if err := runACPTurn(t, cfg, st, sender, "write it"); err != nil {
		t.Fatal(err)
	}
	if got := acpLastAnswer(st); got != "permission: yes" {
		t.Fatalf("answer = %q", got)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.asked) != 1 {
		t.Fatalf("prompts = %d", len(sender.asked))
	}
	p := sender.asked[0]
	var ids []string
	for _, o := range p.Options {
		ids = append(ids, o.OptionID)
	}
	if p.EffectivePermissionMode != config.PermModeAsk || p.ToolCall.Title != "Agent: Write a.txt" || p.ToolCall.Kind != "write" || strings.Join(ids, ",") != "allow,reject" {
		t.Fatalf("prompt = %+v, options %v", p, ids)
	}
	announced := false
	for _, u := range sender.updates {
		if tc, ok := u.(acp.ToolCallUpdate); ok && tc.ToolCallID == p.ToolCall.ToolCallID && tc.Status == "pending" {
			announced = true
		}
	}
	if !announced {
		t.Fatal("the prompt's tool call was not announced as a row first")
	}
}

// An agent that is another Coddy (coddy acp behind an acp row) names its own
// agent's request "Agent: ..." already: the person reads the prefix once.
func TestACPModelNamesANestedAgentsRequestOnce(t *testing.T) {
	cfg, _ := acpLoopConfig(t, acptest.Script{Turns: []acptest.Turn{{Permission: &acptest.Permission{
		Title: "Agent: Edit main.go", Kind: "edit",
		Options: []acptest.Option{
			{ID: "allow", Name: "Allow", Kind: "allow_once"},
			{ID: "reject", Name: "Reject", Kind: "reject_once"},
		},
	}}}})
	st := &session.State{ID: "sess_acp", CWD: t.TempDir(), Mode: session.ModeAgent}
	sender := &acpSender{answer: &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}}
	if err := runACPTurn(t, cfg, st, sender, "edit it"); err != nil {
		t.Fatal(err)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.asked) != 1 || sender.asked[0].ToolCall.Title != "Agent: Edit main.go" {
		t.Fatalf("prompts = %+v", sender.asked)
	}
	for _, u := range sender.updates {
		if tc, ok := u.(acp.ToolCallUpdate); ok && tc.Title != "Agent: Edit main.go" {
			t.Fatalf("tool row title = %q", tc.Title)
		}
	}
}

// A surface's shortcut approval carries no option id (a plan run's sender
// answers {outcome: allow}); the agent still hears its allow option.
func TestACPModelTakesAnApprovalWithoutAnOptionID(t *testing.T) {
	cfg, _ := acpLoopConfig(t, acptest.Script{Turns: []acptest.Turn{{Permission: &acptest.Permission{
		Title: "Run tests", Kind: "execute",
		Options: []acptest.Option{
			{ID: "yes", Name: "Allow once", Kind: "allow_once"},
			{ID: "no", Name: "Reject", Kind: "reject_once"},
		},
	}}}})
	st := &session.State{ID: "sess_acp", CWD: t.TempDir(), Mode: session.ModeAgent}
	if err := runACPTurn(t, cfg, st, &acpSender{answer: &acp.PermissionResult{Outcome: "allow"}}, "run them"); err != nil {
		t.Fatal(err)
	}
	if got := acpLastAnswer(st); got != "permission: yes" {
		t.Fatalf("answer = %q", got)
	}
}

// Plan mode without a mode of the agent mapped to it, and a restricted turn,
// never start the agent.
func TestACPModelRefusesTurnsItCannotHold(t *testing.T) {
	cfg, logPath := acpLoopConfig(t, acptest.Script{})
	cases := map[string]func(*session.State){
		"plan mode with no acp.modes.plan": func(st *session.State) { st.Mode = session.ModePlan },
		"restricted turn": func(st *session.State) {
			st.SetTurnRestriction(&session.TurnRestriction{AllowedTools: []string{"read"}, Note: "admins only"})
		},
	}
	want := map[string]string{
		"plan mode with no acp.modes.plan": "acp.modes.plan",
		"restricted turn":                  "restricted turn",
	}
	for name, setup := range cases {
		st := &session.State{ID: "sess_" + strings.ReplaceAll(name, " ", "_"), CWD: t.TempDir(), Mode: session.ModeAgent}
		setup(st)
		err := runACPTurn(t, cfg, st, &acpSender{}, "hello")
		said := acpLastAnswer(st)
		if err != nil {
			said += " " + err.Error()
		}
		if !strings.Contains(said, want[name]) {
			t.Errorf("%s: the refusal does not name %q: %q", name, want[name], said)
		}
	}
	if n := len(acptest.Methods(acptest.ReadLog(t, logPath), "initialize")); n != 0 {
		t.Fatalf("a refused turn started the agent (%d initialize)", n)
	}
}

// The first-token timer guards a model's request; an agent's prompt may work
// in silence for minutes and is bounded by its own silence timer instead.
func TestACPModelIsExemptFromTheFirstTokenTimer(t *testing.T) {
	cfg, _ := acpLoopConfig(t, acptest.Script{Turns: []acptest.Turn{{Text: []string{"late"}, DelayMS: 400}}})
	ms := 100
	cfg.Agent.LLMFirstTokenTimeoutMS = &ms
	st := &session.State{ID: "sess_acp", CWD: t.TempDir(), Mode: session.ModeAgent}
	if err := runACPTurn(t, cfg, st, &acpSender{}, "take your time"); err != nil {
		t.Fatal(err)
	}
	if got := acpLastAnswer(st); got != "late" {
		t.Fatalf("answer = %q", got)
	}
}

// Compaction never runs on an acp model: the agent keeps its own context,
// and a summary request would be a task it runs with its own tools.
func TestACPModelIsNotASummarizer(t *testing.T) {
	cfg, logPath := acpLoopConfig(t, acptest.Script{})
	st := seededCompactState(t, 3)
	ag := NewAgent(cfg, st, &acpSender{}, nil)
	if ag.maybeAutoCompact(context.Background()) {
		t.Fatal("auto compaction ran on an acp model")
	}
	_, err := ag.CompactSession(context.Background(), CompactOptions{Force: true})
	if err == nil || !strings.Contains(err.Error(), "compaction.model") {
		t.Fatalf("err = %v, want the refusal to name compaction.model", err)
	}
	if n := len(acptest.Methods(acptest.ReadLog(t, logPath), "initialize")); n != 0 {
		t.Fatalf("compaction started the agent (%d initialize)", n)
	}
}

// storedACPSession is a session whose bundle is on disk, as on every surface
// but a print run: the agent's tool output is written there.
func storedACPSession(t *testing.T, id string) *session.State {
	t.Helper()
	root := t.TempDir()
	dir, err := (&session.FileStore{Root: root}).EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	return &session.State{ID: id, CWD: t.TempDir(), Mode: session.ModeAgent, SessionDir: dir}
}

// The agent's tool calls are tool rows of their own on the surface - its
// title, its input, its outcome with the first lines of its output - the
// answer keeps a preview of each, the session's tool_calls folder the whole
// output, and none of it goes into the reasoning or becomes a Coddy call.
func TestACPModelShowsTheAgentsToolsAsRows(t *testing.T) {
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	output := strings.Join(lines, "\n")
	cfg, _ := acpLoopConfig(t, acptest.Script{Turns: []acptest.Turn{{
		Thoughts: []string{"looking"},
		Tools:    []acptest.Tool{{ID: "t1", Title: "Read README.md", Kind: "read", Input: map[string]string{"file_path": "README.md"}, Output: output}},
		Text:     []string{"done"},
	}}})
	st := storedACPSession(t, "sess_rows")
	sender := &acpSender{}
	if err := runACPTurn(t, cfg, st, sender, "read it"); err != nil {
		t.Fatal(err)
	}
	sender.mu.Lock()
	var announced *acp.ToolCallUpdate
	var input, end *acp.ToolCallStatusUpdate
	for _, u := range sender.updates {
		switch v := u.(type) {
		case acp.ToolCallUpdate:
			if v.ToolCallID == "acp_t1" {
				announced = &v
			}
		case acp.ToolCallStatusUpdate:
			if v.ToolCallID != "acp_t1" {
				continue
			}
			if v.Status == "in_progress" {
				input = &v
			} else {
				end = &v
			}
		}
	}
	sender.mu.Unlock()
	if announced == nil || announced.Title != "Read README.md" || announced.Kind != "read" || announced.Status != "pending" {
		t.Fatalf("row = %+v", announced)
	}
	if input == nil || len(input.Content) != 1 || input.Content[0].Content.Text != `{"file_path":"README.md"}` {
		t.Fatalf("input = %+v", input)
	}
	if end == nil || end.Status != "completed" || len(end.Content) != 1 || !strings.HasPrefix(end.Content[0].Content.Text, "line 1\n") || strings.Contains(end.Content[0].Content.Text, "line 30") {
		t.Fatalf("outcome = %+v", end)
	}
	if preview, _ := end.Meta["coddy"].(map[string]interface{})["toolResultPreview"].(map[string]interface{}); preview["totalLines"] != 30 {
		t.Fatalf("outcome meta = %+v", end.Meta)
	}
	msgs := st.GetMessages()
	answer := msgs[len(msgs)-1]
	if answer.Role != llm.RoleAssistant || answer.Content != "done" || answer.Reasoning != "looking" || len(answer.ToolCalls) != 0 {
		t.Fatalf("answer = %+v", answer)
	}
	if len(answer.AgentTools) != 1 {
		t.Fatalf("agent tools = %+v", answer.AgentTools)
	}
	kept := answer.AgentTools[0]
	if kept.ID != "acp_t1" || kept.Title != "Read README.md" || kept.Kind != "read" || kept.Status != "completed" || kept.OutputLines != 30 || strings.Contains(kept.Output, "line 30") {
		t.Fatalf("kept = %+v", kept)
	}
	full, err := session.ReadToolCallResult(st.SessionDir, "acp_t1")
	if err != nil || strings.TrimRight(full, "\n") != output {
		t.Fatalf("result.md = %q, %v", full, err)
	}
	meta, err := session.ReadToolCallMeta(st.SessionDir, "acp_t1")
	if err != nil || meta.Name != "Read README.md" || meta.Status != "completed" || meta.StartedAt == "" || meta.FinishedAt == "" {
		t.Fatalf("meta = %+v, %v", meta, err)
	}
}

// A call the agent left open when the person stopped the turn ends as
// cancelled, live and in the kept answer, rather than spinning forever.
func TestACPModelEndsAnOpenAgentToolOnStop(t *testing.T) {
	cfg, _ := acpLoopConfig(t, acptest.Script{Turns: []acptest.Turn{{
		Tools:      []acptest.Tool{{ID: "t1", Title: "Run go test ./...", Open: true}},
		Text:       []string{"working"},
		WaitCancel: true,
	}}})
	st := storedACPSession(t, "sess_stop")
	sender := &acpSender{}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sender.onText = cancel
	_, _ = NewAgent(cfg, st, sender, nil).Run(ctx, []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "run them"}})
	sender.mu.Lock()
	var last string
	for _, u := range sender.updates {
		if v, ok := u.(acp.ToolCallStatusUpdate); ok && v.ToolCallID == "acp_t1" {
			last = v.Status
		}
	}
	sender.mu.Unlock()
	if last != "cancelled" {
		t.Fatalf("the open call ended as %q", last)
	}
	msgs := st.GetMessages()
	answer := msgs[len(msgs)-1]
	if len(answer.AgentTools) != 1 || answer.AgentTools[0].Status != "cancelled" {
		t.Fatalf("kept answer = %+v", answer)
	}
}
