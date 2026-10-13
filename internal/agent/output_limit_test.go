package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// What the loop does with a step the model's output limit (max_tokens) ended
// before the model was done, and the helpers the output-limit tests share. This
// file holds the step that produced nothing the agent can act on, which is
// asked again in words (outputLimitNudge) and left out of every request; the
// bound and the agent.llm_retry_max slot the three kinds of cut share; and the
// history a later request replays without the steps that came back empty. A
// text cut off partway is in output_limit_partial_test.go, a tool call cut off
// partway in output_limit_cutcall_test.go, and the recoveries a rebuilt or
// rerun request has to keep in output_limit_recovery_test.go. The happy path is
// features/turn_end_reasons.feature; these are its edges.

// The notices of a turn the output limit ended. They are fixed texts: the
// bound they follow is held by the number of requests a turn makes.
const (
	plainOutputLimitNotice    = "The answer was cut off at the model's output limit (max_tokens). Send a message to let the agent continue, or raise the model's max_tokens."
	subagentOutputLimitNotice = "The subagent's answer was cut off at the model's output limit (max_tokens). Its report may be incomplete: raise the model's max_tokens or run it again."
)

// outputLimitStep is what one call of outputLimitProvider does.
type outputLimitStep struct {
	reasoning string
	// signature signs the reasoning, as Anthropic extended thinking does.
	signature string
	text      string
	stop      string
	// calls are the tool calls the step asks for.
	calls []llm.ToolCall
	// err fails the call after its text and reasoning were streamed.
	err error
	// during runs in the middle of the call, before it answers.
	during func()
}

// outputLimitProvider answers its n-th call with steps[n] (the last step
// repeats) and records every request it was sent. A compaction's summarizer
// gets "Earlier turn summary." back.
type outputLimitProvider struct {
	steps []outputLimitStep
	calls int
	seen  [][]llm.Message
}

func (p *outputLimitProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return &llm.Response{Content: "Earlier turn summary."}, nil
}

func (p *outputLimitProvider) Stream(_ context.Context, msgs []llm.Message, _ []llm.ToolDefinition, emit func(llm.StreamChunk)) (*llm.Response, error) {
	step := p.steps[min(p.calls, len(p.steps)-1)]
	p.calls++
	p.seen = append(p.seen, append([]llm.Message(nil), msgs...))
	if step.during != nil {
		step.during()
	}
	if step.reasoning != "" {
		emit(llm.StreamChunk{ReasoningDelta: step.reasoning})
	}
	if step.text != "" {
		emit(llm.StreamChunk{TextDelta: step.text})
	}
	if step.err != nil {
		return nil, step.err
	}
	return &llm.Response{Content: step.text, Reasoning: step.reasoning, ReasoningSignature: step.signature, ToolCalls: step.calls, StopReason: step.stop, OutputTokens: 7}, nil
}

func outputLimitAgent(t *testing.T, p llm.Provider, adjust func(*config.Config)) (*Agent, *session.State) {
	t.Helper()
	return outputLimitAgentWith(t, p, adjust, resumePermissionSender{})
}

// outputLimitAgentWith is outputLimitAgent talking to a client of the test's
// choosing, for a test that reads what the turn sent it.
func outputLimitAgentWith(t *testing.T, p llm.Provider, adjust func(*config.Config), sender acp.UpdateSender) (*Agent, *session.State) {
	t.Helper()
	st := &session.State{ID: "sess_output_limit", CWD: t.TempDir(), Mode: session.ModeAgent, SessionDir: t.TempDir()}
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 20},
	}
	if adjust != nil {
		adjust(cfg)
	}
	ag := NewAgent(cfg, st, sender, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return p, nil }
	return ag, st
}

// cutCallAgent is an outputLimitAgent whose workspace holds a notes file of nine
// numbered lines and whose client records what it is sent. The agent's
// permission mode is the default one, which asks for a write.
func cutCallAgent(t *testing.T, p llm.Provider, adjust func(*config.Config)) (*Agent, *session.State, *outputLimitSender) {
	t.Helper()
	sender := &outputLimitSender{}
	ag, st := outputLimitAgentWith(t, p, adjust, sender)
	var lines []string
	for i := 1; i <= 9; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	if err := os.WriteFile(filepath.Join(st.CWD, "notes.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return ag, st, sender
}

func runOutputLimitTurn(ag *Agent) (string, error) {
	return ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "write the module"}})
}

// llmRetryMax sets agent.llm_retry_max.
func llmRetryMax(n int) func(*config.Config) {
	return func(c *config.Config) { c.Agent.LLMRetryMax = &n }
}

// armedCompaction is the config change of a session whose history is compacted
// before the request after the step that calls arm, and arm itself. The session
// needs an earlier turn (addEarlierTurn) for the compaction to fold.
func armedCompaction() (adjust func(*config.Config), arm func()) {
	enabled, keep := false, 1
	adjust = func(c *config.Config) {
		c.Compaction = config.Compaction{Enabled: &enabled, ThresholdPercent: 1, KeepRecentTurns: &keep}
		c.Models[0].MaxContextTokens = 10000
	}
	return adjust, func() { enabled = true }
}

// addEarlierTurn gives a session a turn before the one under test, which a
// compaction folds into "Earlier turn summary.".
func addEarlierTurn(st *session.State) {
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "Earlier question."})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Earlier answer."})
}

// compacted says whether a request carries the summary of a compaction, which
// a test of a rebuilt request checks so that it does not pass by proving nothing.
func compacted(msgs []llm.Message) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, "Earlier turn summary.") {
			return true
		}
	}
	return false
}

// The three kinds of step the output limit cuts off, and what the turn's other
// steps do.
var (
	// cutOff is a step that only thought before the limit stopped it.
	cutOff = outputLimitStep{reasoning: "Thinking about the whole file first.", stop: "max_tokens"}
	// partialStep is a step that wrote visible text and ran out of output budget
	// in the middle of it, after some thinking.
	partialStep = outputLimitStep{reasoning: "Plan.", text: partialText, stop: "max_tokens"}
	answered    = outputLimitStep{text: "Done.", stop: "end_turn"}
)

const partialText = "Here is the first half of the module and then"

const cutWritePath = "module.go"

// cutWrite is a write call whose arguments stop inside the content string.
func cutWrite(id string) llm.ToolCall {
	return llm.ToolCall{ID: id, Name: "write", InputJSON: `{"path":"` + cutWritePath + `","content":"package module\n\nfunc Run() {`}
}

// cutWriteStep is a step that said something, asked for calls and was cut off
// in the middle of the last of them.
func cutWriteStep(text string, calls ...llm.ToolCall) outputLimitStep {
	return outputLimitStep{text: text, calls: calls, stop: "max_tokens"}
}

// outputLimitSender is a client that records what a turn sent it: the session
// updates and the permission requests.
type outputLimitSender struct {
	mu      sync.Mutex
	updates []interface{}
	asked   []acp.PermissionRequestParams
}

func (s *outputLimitSender) SendSessionUpdate(_ string, u interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, u)
	return nil
}

func (s *outputLimitSender) RequestPermission(_ context.Context, p acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, p)
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}

func (*outputLimitSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

// permissionRequests is how many times the client was asked to approve a call.
func (s *outputLimitSender) permissionRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.asked)
}

// toolStatuses lists, in order, the statuses the client was told for a tool call.
func (s *outputLimitSender) toolStatuses(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, u := range s.updates {
		if st, ok := u.(acp.ToolCallStatusUpdate); ok && st.ToolCallID == id {
			out = append(out, st.Status)
		}
	}
	return out
}

// requestHistory is what a request carries below the trailing turn context
// block, as role and text.
func requestHistory(msgs []llm.Message) []string {
	if n := len(msgs); n > 0 && msgs[n-1].Role == llm.RoleUser && strings.HasPrefix(msgs[n-1].Content, turnContextOpenTag) {
		msgs = msgs[:n-1]
	}
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = string(m.Role) + ": " + m.Content
	}
	return out
}

// tailLines is the last n lines of a history joined with newlines, for a
// failure message that has to stay readable under a system prompt.
func tailLines(joined string, n int) string {
	lines := strings.Split(joined, "\n")
	return strings.Join(lines[max(len(lines)-n, 0):], "\n")
}

func countNudges(msgs []llm.Message, text string) int {
	var n int
	for _, m := range msgs {
		if m.Role == llm.RoleUser && m.Content == text {
			n++
		}
	}
	return n
}

func hasEmptyAssistant(msgs []llm.Message) bool {
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
			return true
		}
	}
	return false
}

func assistantMessages(msgs []llm.Message) []llm.Message {
	var out []llm.Message
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant {
			out = append(out, m)
		}
	}
	return out
}

// textMessages lists the assistant messages of msgs that hold text.
func textMessages(msgs []llm.Message) []llm.Message {
	var out []llm.Message
	for _, m := range assistantMessages(msgs) {
		if strings.TrimSpace(m.Content) != "" {
			out = append(out, m)
		}
	}
	return out
}

// assistantTexts is the text of every assistant message of msgs that holds
// text, in order.
func assistantTexts(msgs []llm.Message) []string {
	var out []string
	for _, m := range textMessages(msgs) {
		out = append(out, m.Content)
	}
	return out
}

func requireNoAssistantInARow(t *testing.T, msgs []llm.Message) {
	t.Helper()
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == llm.RoleAssistant && msgs[i-1].Role == llm.RoleAssistant {
			t.Fatalf("two assistant messages in a row at %d:\n%s", i, tailLines(strings.Join(requestHistory(msgs), "\n"), 6))
		}
	}
}

// callIDs lists the ids of the tool calls the assistant messages of msgs make.
func callIDs(msgs []llm.Message) []string {
	var ids []string
	for _, m := range assistantMessages(msgs) {
		for _, tc := range m.ToolCalls {
			ids = append(ids, tc.ID)
		}
	}
	return ids
}

func requireWholePairing(t *testing.T, msgs []llm.Message) {
	t.Helper()
	if issues := session.ValidateToolPairing(msgs); len(issues) > 0 {
		t.Fatalf("the tool-call pairing is broken: %+v", issues)
	}
}

func requireNoFile(t *testing.T, st *session.State, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(st.CWD, name)); err == nil {
		t.Fatalf("%s was written by a call that was cut off", name)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// TestPromptCacheOutputLimitNudgeIsLocalAndAppendOnly: the corrective message
// reaches the model only, after the history the previous request sent, with the
// cut-off step left out of it; the transcript keeps the step and never the
// message, and the turn ends with the answer and no notice.
func TestPromptCacheOutputLimitNudgeIsLocalAndAppendOnly(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{cutOff, answered}}
	ag, st := outputLimitAgent(t, p, nil)

	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop = %q, err = %v; want end_turn", stop, err)
	}
	if p.calls != 2 {
		t.Fatalf("provider called %d times, want 2", p.calls)
	}
	if got := countNudges(p.seen[0], outputLimitNudge); got != 0 {
		t.Fatalf("the first request already carries the nudge")
	}
	if got := countNudges(p.seen[1], outputLimitNudge); got != 1 {
		t.Fatalf("the second request carries the nudge %d times, want 1", got)
	}
	if hasEmptyAssistant(p.seen[1]) {
		t.Fatalf("the cut-off step was replayed to the model: %+v", p.seen[1])
	}
	first, second := requestHistory(p.seen[0]), requestHistory(p.seen[1])
	if len(second) != len(first)+1 {
		t.Fatalf("history went from %d to %d messages, want one more (the nudge)", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("message %d of the cached history changed:\nbefore: %s\nafter:  %s", i, first[i], second[i])
		}
	}
	if !strings.HasSuffix(second[len(second)-1], outputLimitNudge) {
		t.Fatalf("the nudge is not the last history message: %q", second[len(second)-1])
	}

	var keptStep bool
	for _, m := range st.GetMessages() {
		if m.Content == outputLimitNudge {
			t.Fatal("the nudge was written to the transcript")
		}
		if m.Role == llm.RoleAssistant && strings.TrimSpace(m.Content) == "" && strings.Contains(m.Reasoning, "whole file first") {
			keptStep = true
		}
	}
	if !keptStep {
		t.Fatal("the transcript lost the step the user watched think")
	}
	if notice := st.TakeTurnStopNotice(); notice != "" {
		t.Fatalf("a recovered turn left a stop notice: %q", notice)
	}
}

// TestOutputLimitCountIsOfConsecutiveCutSteps: maxOutputLimitRecoveries bounds
// the cut-off steps in a row. A step the limit did not stop ends the run and
// starts the count over even when it is answered by another recovery, as an
// empty answer is: the cut steps after it are asked again as the first ones
// were, and only the retry allowance bounds them all together.
func TestOutputLimitCountIsOfConsecutiveCutSteps(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps []outputLimitStep
	}{
		{"steps that only thought", []outputLimitStep{cutOff, unsignedEmptyStep, cutOff, cutOff, answered}},
		{"texts cut off", []outputLimitStep{partialStep, unsignedEmptyStep, partialStep, partialStep, answered}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &outputLimitProvider{steps: tc.steps}
			ag, st := outputLimitAgent(t, p, llmRetryMax(10))
			stop, err := runOutputLimitTurn(ag)
			if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != len(tc.steps) {
				t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after %d", stop, err, p.calls, len(tc.steps))
			}
			if notice := st.TakeTurnStopNotice(); notice != "" {
				t.Fatalf("a recovered turn left a notice: %q", notice)
			}
		})
	}
}

// TestOutputLimitRecoveryIsBounded holds the bound the three kinds of cut share
// (maxOutputLimitRecoveries): whatever the output limit cut off - a step that
// only thought, a text, a tool call - the model is asked again at most that many
// times in a row, each time out of a slot of agent.llm_retry_max (issue #226),
// only when an iteration is left to read the message in, and never behind the
// back of a user who pressed Stop. When it may not ask, the turn ends on the
// output limit with its notice, not on the step limit; a subagent has nobody to
// type "continue", so the recovery is what keeps its run going, and its notice
// does not tell a transcript that takes no message to "send a message". Every
// request after the first carries one more message of the kind the step was
// answered with and nothing the step cut off, and a cut call is never run or
// offered for approval.
func TestOutputLimitRecoveryIsBounded(t *testing.T) {
	kinds := []struct {
		name  string
		step  outputLimitStep
		nudge string
		// text says the step's text stays in the history, the user having read it.
		text bool
	}{
		{"a step that only thought", cutOff, outputLimitNudge, false},
		{"a text", partialStep, outputLimitContinueNudge, true},
		{"a tool call", cutWriteStep("", cutWrite("call_w")), cutOffToolCallNudge("write"), false},
	}
	situations := []struct {
		name     string
		adjust   func(*config.Config)
		subagent bool
		// stopped: the user presses Stop while the cut-off step streams.
		stopped bool
		// answers: the step after the cut-off one answers; otherwise every step
		// is cut off the same way.
		answers bool
		calls   int
		stop    acp.StopReason
		// notice: the turn ends with the notice of the output limit.
		notice bool
	}{
		{name: "cut off every time", calls: 1 + maxOutputLimitRecoveries, stop: acp.StopReasonMaxTokens, notice: true},
		{name: "llm_retry_max 0", adjust: llmRetryMax(0), calls: 1, stop: acp.StopReasonMaxTokens, notice: true},
		{name: "llm_retry_max 1", adjust: llmRetryMax(1), calls: 2, stop: acp.StopReasonMaxTokens, notice: true},
		{name: "at the step cap", adjust: func(c *config.Config) { c.Agent.MaxTurns = 1 }, answers: true, calls: 1, stop: acp.StopReasonMaxTokens, notice: true},
		{name: "stopped by the user", stopped: true, answers: true, calls: 1, stop: acp.StopReasonCancelled},
		{name: "subagent that recovers", subagent: true, answers: true, calls: 2, stop: acp.StopReasonEndTurn},
		{name: "subagent cut off every time", subagent: true, calls: 1 + maxOutputLimitRecoveries, stop: acp.StopReasonMaxTokens, notice: true},
	}
	for _, kind := range kinds {
		for _, sit := range situations {
			t.Run(kind.name+"/"+sit.name, func(t *testing.T) {
				var st *session.State
				step := kind.step
				if sit.stopped {
					step.during = func() { st.SetUserCancelledTurn() }
				}
				steps := []outputLimitStep{step}
				if sit.answers {
					steps = append(steps, answered)
				}
				p := &outputLimitProvider{steps: steps}
				var ag *Agent
				var sender *outputLimitSender
				ag, st, sender = cutCallAgent(t, p, sit.adjust)
				if sit.subagent {
					ag.subagent = &session.SubagentMeta{Name: "general"}
				}

				stop, err := runOutputLimitTurn(ag)
				if err != nil || stop != string(sit.stop) || p.calls != sit.calls {
					t.Fatalf("stop = %q, err = %v after %d calls; want %s after %d", stop, err, p.calls, sit.stop, sit.calls)
				}
				want := ""
				switch {
				case sit.notice && sit.subagent:
					want = subagentOutputLimitNotice
				case sit.notice:
					want = plainOutputLimitNotice
				}
				if notice := st.TakeTurnStopNotice(); notice != want {
					t.Fatalf("notice = %q, want %q", notice, want)
				}

				// The messages pile up in the order they were earned, and what the
				// steps cut off never comes back.
				last := p.seen[len(p.seen)-1]
				if got := countNudges(last, kind.nudge); got != sit.calls-1 {
					t.Fatalf("the last request carries the message %d times, want %d", got, sit.calls-1)
				}
				if hasEmptyAssistant(last) {
					t.Fatal("a cut-off step was replayed to the model")
				}
				if ids := callIDs(last); len(ids) != 0 {
					t.Fatalf("the last request replays tool calls %v", ids)
				}
				texts := 0
				if kind.text {
					// Every text but the one the last request asks for.
					texts = sit.calls - 1
					if got := len(assistantTexts(st.GetMessages())); got != sit.calls {
						t.Fatalf("the transcript holds %d texts, want %d", got, sit.calls)
					}
				}
				if got := len(assistantTexts(last)); got != texts {
					t.Fatalf("the last request replays %d texts, want %d", got, texts)
				}
				requireNoFile(t, st, cutWritePath)
				requireWholePairing(t, st.GetMessages())
				if n := sender.permissionRequests(); n != 0 {
					t.Fatalf("the client was asked %d times to approve a call that never ran", n)
				}
			})
		}
	}
}

// TestOutputLimitWhitespaceOnlyTextIsNothing: text that trims to empty is no
// answer, so the step is asked again.
func TestOutputLimitWhitespaceOnlyTextIsNothing(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{{text: " \n\t", stop: "max_tokens"}, answered}}
	ag, _ := outputLimitAgent(t, p, nil)
	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 2 {
		t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 2", stop, err, p.calls)
	}
}

// TestOutputLimitRecoveryThenEmptyAnswerReplaysFirst: the two recoveries count
// together. After the output-limit nudge, an empty answer still gets the plain
// replay of the request that failed - nudge included, exactly once - before any
// wording nudge.
func TestOutputLimitRecoveryThenEmptyAnswerReplaysFirst(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{cutOff, {reasoning: "Hmm.", stop: "end_turn"}, answered}}
	ag, _ := outputLimitAgent(t, p, nil)
	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 3 {
		t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 3", stop, err, p.calls)
	}
	second, third := requestHistory(p.seen[1]), requestHistory(p.seen[2])
	if strings.Join(second, "\n") != strings.Join(third, "\n") {
		t.Fatalf("the replay is not the request that failed:\nsecond: %q\nthird:  %q", second, third)
	}
	if got := countNudges(p.seen[2], outputLimitNudge); got != 1 {
		t.Fatalf("the replay carries the output-limit nudge %d times, want 1", got)
	}
	if got := countNudges(p.seen[2], emptyAssistantContinuationNudge); got != 0 {
		t.Fatalf("the wording nudge came before the plain replay")
	}
}

// A step that came back empty stays in the transcript, where the user watched
// it think. The history a later request replays from that transcript must not
// carry it: an assistant turn with nothing to say is no turn of the
// conversation, and two assistant messages in a row are refused by chat
// templates that insist on alternation.

// TestEmptyAssistantStepsAreNotReplayed pins what buildMessages drops: an
// assistant message with no text, no tool call and no signed reasoning, whatever
// unsigned thinking it holds. Signed reasoning is what Anthropic, Codex and
// Devin replay, so a message that carries it stays.
func TestEmptyAssistantStepsAreNotReplayed(t *testing.T) {
	st := &session.State{ID: "sess_replay", CWD: t.TempDir(), Mode: session.ModeAgent}
	for _, m := range []llm.Message{
		{Role: llm.RoleUser, Content: "write the module"},
		{Role: llm.RoleAssistant, Reasoning: "thinking that was cut off"},
		{Role: llm.RoleAssistant, Content: " \n\t"},
		{Role: llm.RoleAssistant, Content: "On it.", ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "coddy_todo_plan_read", InputJSON: "{}"}}},
		{Role: llm.RoleTool, Content: "no plan", ToolCallID: "call_1"},
		{Role: llm.RoleAssistant, Reasoning: "signed", ReasoningSignature: "sig"},
		{Role: llm.RoleAssistant, Content: "Done."},
	} {
		st.AddMessage(m)
	}
	ag := NewAgent(&config.Config{Agent: config.Agent{Model: "fake/model"}}, st, resumePermissionSender{}, nil)
	got := requestHistory(ag.buildMessages("SYSTEM"))
	want := []string{
		"system: SYSTEM",
		"user: write the module",
		"assistant: On it.",
		"tool: no plan",
		"assistant: ",
		"assistant: Done.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("replayed history:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestPromptCacheNextTurnReplaysNoEmptyStep: after a turn that recovered from a
// cut-off step, the next turn's request is the conversation as the model saw it
// in the turn itself - the user's prompt, the tool round, the answer - with no
// empty assistant turn in it and never two assistant messages in a row. Every
// request leaves the step out by the same rule, so the prefix a provider cached
// during the turn is still the prefix on the next one.
func TestPromptCacheNextTurnReplaysNoEmptyStep(t *testing.T) {
	read := llm.ToolCall{ID: "call_read", Name: "coddy_todo_plan_read", InputJSON: "{}"}
	p := &outputLimitProvider{steps: []outputLimitStep{
		cutOff,
		{text: "Reading the plan.", calls: []llm.ToolCall{read}, stop: "tool_use"},
		answered,
		{text: "Tested.", stop: "end_turn"},
	}}
	ag, st := outputLimitAgent(t, p, nil)

	if stop, err := runOutputLimitTurn(ag); err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("first turn: stop = %q, err = %v", stop, err)
	}
	if p.calls != 3 {
		t.Fatalf("first turn: provider called %d times, want 3", p.calls)
	}
	var cut bool
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleAssistant && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 && strings.Contains(m.Reasoning, "whole file first") {
			cut = true
		}
	}
	if !cut {
		t.Fatal("the transcript lost the cut-off step")
	}

	if stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "now test it"}}); err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("second turn: stop = %q, err = %v", stop, err)
	}
	if p.calls != 4 {
		t.Fatalf("second turn: provider called %d times in all, want 4", p.calls)
	}
	hist := requestHistory(p.seen[3])
	want := []string{
		"user: write the module",
		"assistant: Reading the plan.",
		"tool: ",
		"assistant: Done.",
		"user: now test it",
	}
	// The system prompt first, then the conversation; the tool result's text is
	// whatever the plan tool answered, so only its place is compared.
	if len(hist) != len(want)+1 {
		t.Fatalf("second turn replays %d messages, want %d:\n%s", len(hist), len(want)+1, strings.Join(hist, "\n"))
	}
	for i, w := range want {
		if !strings.HasPrefix(hist[i+1], strings.TrimRight(w, " ")) {
			t.Fatalf("message %d = %q, want it to start with %q", i+1, hist[i+1], w)
		}
	}
	requireNoAssistantInARow(t, p.seen[3])
}
