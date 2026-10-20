package agent

import (
	"context"
	"encoding/json"
	"flag"
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

	mu      sync.Mutex
	updates []interface{}
	asked   []acp.PermissionRequestParams
}

func (s *acpSender) SendSessionUpdate(_ string, update interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, update)
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
