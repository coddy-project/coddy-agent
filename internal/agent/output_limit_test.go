package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A step the model's output limit cut off before it produced anything the
// agent can act on is asked again in words, a bounded number of times, instead
// of ending the turn on a notice and a user who has to type "continue". The
// happy path is features/turn_end_reasons.feature; these are its edges.

const plainOutputLimitNotice = "The answer was cut off at the model's output limit (max_tokens). Send a message to let the agent continue, or raise the model's max_tokens."

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
// repeats) and records every request it was sent.
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
	st := &session.State{ID: "sess_output_limit", CWD: t.TempDir(), Mode: session.ModeAgent, SessionDir: t.TempDir()}
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 20},
	}
	if adjust != nil {
		adjust(cfg)
	}
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return p, nil }
	return ag, st
}

func runOutputLimitTurn(ag *Agent) (string, error) {
	return ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "write the module"}})
}

var cutOff = outputLimitStep{reasoning: "Thinking about the whole file first.", stop: "max_tokens"}
var answered = outputLimitStep{text: "Done.", stop: "end_turn"}

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

// TestOutputLimitRecoveryIsBoundedAndTheNoticeSaysSo: a model that keeps being
// cut off costs the turn maxOutputLimitRecoveries extra requests, and the
// notice then says that the model was already asked.
func TestOutputLimitRecoveryIsBoundedAndTheNoticeSaysSo(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{cutOff}}
	ag, st := outputLimitAgent(t, p, nil)

	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonMaxTokens) {
		t.Fatalf("stop = %q, err = %v; want max_tokens", stop, err)
	}
	if want := 1 + maxOutputLimitRecoveries; p.calls != want {
		t.Fatalf("provider called %d times, want %d (the step and its recoveries)", p.calls, want)
	}
	// The messages pile up in the order they were earned and the cut-off steps
	// never come back.
	if got := countNudges(p.seen[len(p.seen)-1], outputLimitNudge); got != maxOutputLimitRecoveries {
		t.Fatalf("the last request carries %d nudges, want %d", got, maxOutputLimitRecoveries)
	}
	if hasEmptyAssistant(p.seen[len(p.seen)-1]) {
		t.Fatal("a cut-off step was replayed to the model")
	}
	notice := st.TakeTurnStopNotice()
	for _, want := range []string{"max_tokens", "2 times", "keep its steps short", "Send a message"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice %q does not say %q", notice, want)
		}
	}
}

// TestOutputLimitRecoveryHonoursLLMRetryMax follows issue #226: the recovery
// takes a slot of agent.llm_retry_max, so 0 turns it off and 1 allows one.
func TestOutputLimitRecoveryHonoursLLMRetryMax(t *testing.T) {
	for _, tc := range []struct {
		name       string
		retryMax   int
		wantCalls  int
		wantNotice string
	}{
		{"off", 0, 1, plainOutputLimitNotice},
		{"one slot", 1, 2, "after the agent had already asked the model once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &outputLimitProvider{steps: []outputLimitStep{cutOff}}
			ag, st := outputLimitAgent(t, p, func(c *config.Config) { c.Agent.LLMRetryMax = &tc.retryMax })
			stop, err := runOutputLimitTurn(ag)
			if err != nil || stop != string(acp.StopReasonMaxTokens) {
				t.Fatalf("stop = %q, err = %v; want max_tokens", stop, err)
			}
			if p.calls != tc.wantCalls {
				t.Fatalf("provider called %d times, want %d", p.calls, tc.wantCalls)
			}
			if notice := st.TakeTurnStopNotice(); !strings.Contains(notice, tc.wantNotice) {
				t.Fatalf("notice = %q, want it to contain %q", notice, tc.wantNotice)
			}
		})
	}
}

// TestOutputLimitWithVisibleTextEndsTheTurn: a step that stopped at the limit
// after writing something the user can read is a cut answer, not an empty
// step; the turn ends on it as before, with the plain notice.
func TestOutputLimitWithVisibleTextEndsTheTurn(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{{reasoning: "Plan.", text: "Here is the first half of the module and then", stop: "max_tokens"}, answered}}
	ag, st := outputLimitAgent(t, p, nil)
	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonMaxTokens) || p.calls != 1 {
		t.Fatalf("stop = %q, err = %v after %d calls; want max_tokens after 1", stop, err, p.calls)
	}
	if notice := st.TakeTurnStopNotice(); notice != plainOutputLimitNotice {
		t.Fatalf("notice = %q", notice)
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

// TestOutputLimitRecoveryLeavesAStoppedTurnAlone: a Stop pressed during the cut
// step ends the turn; the model is not asked again behind the user's back.
func TestOutputLimitRecoveryLeavesAStoppedTurnAlone(t *testing.T) {
	var st *session.State
	stopped := cutOff
	stopped.during = func() { st.SetUserCancelledTurn() }
	p := &outputLimitProvider{steps: []outputLimitStep{stopped, answered}}
	var ag *Agent
	ag, st = outputLimitAgent(t, p, nil)
	_, _ = runOutputLimitTurn(ag)
	if p.calls != 1 {
		t.Fatalf("provider called %d times after a Stop, want 1", p.calls)
	}
}

// TestOutputLimitAtTheStepCapEndsOnTheOutputLimit: the nudge needs an iteration
// to be read in. On the last allowed step the turn does not pretend the step
// limit was what stopped it: it ends on the output limit, with that notice and
// without spending a retry slot on a request that cannot be sent.
func TestOutputLimitAtTheStepCapEndsOnTheOutputLimit(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{cutOff, answered}}
	ag, st := outputLimitAgent(t, p, func(c *config.Config) { c.Agent.MaxTurns = 1 })
	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonMaxTokens) || p.calls != 1 {
		t.Fatalf("stop = %q, err = %v after %d calls; want max_tokens after 1", stop, err, p.calls)
	}
	if notice := st.TakeTurnStopNotice(); notice != plainOutputLimitNotice {
		t.Fatalf("notice = %q, want the plain output-limit notice", notice)
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

// TestOutputLimitRecoverySurvivesCompaction: a compaction between the cut-off
// step and its retry rebuilds the history from the transcript, which still holds
// the empty step; the rebuilt request leaves it out and carries the nudge once.
func TestOutputLimitRecoverySurvivesCompaction(t *testing.T) {
	enabled, keep := false, 1
	p := &outputLimitProvider{}
	p.steps = []outputLimitStep{
		{reasoning: cutOff.reasoning, stop: "max_tokens", during: func() { enabled = true }},
		answered,
	}
	ag, st := outputLimitAgent(t, p, func(c *config.Config) {
		c.Compaction = config.Compaction{Enabled: &enabled, ThresholdPercent: 1, KeepRecentTurns: &keep}
		c.Models[0].MaxContextTokens = 10000
	})
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "Earlier question."})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Earlier answer."})

	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 2 {
		t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 2", stop, err, p.calls)
	}
	if got := countNudges(p.seen[1], outputLimitNudge); got != 1 {
		t.Fatalf("the request after the compaction carries the nudge %d times, want 1", got)
	}
	if hasEmptyAssistant(p.seen[1]) {
		t.Fatal("the compaction resurrected the cut-off step")
	}
	var summarized bool
	for _, m := range p.seen[1] {
		if strings.Contains(m.Content, "Earlier turn summary.") {
			summarized = true
		}
	}
	if !summarized {
		t.Fatal("the history was not compacted between the two requests; the test proves nothing")
	}
}

// TestOutputLimitRecoveryInASubagent: a child has nobody to type "continue", so
// the recovery is what keeps its run going; when it gives up, the notice does
// not tell a transcript that takes no message to "send a message".
func TestOutputLimitRecoveryInASubagent(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{cutOff, answered}}
	ag, st := outputLimitAgent(t, p, nil)
	ag.subagent = &session.SubagentMeta{Name: "general"}
	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 2 {
		t.Fatalf("subagent: stop = %q, err = %v after %d calls; want end_turn after 2", stop, err, p.calls)
	}
	if notice := st.TakeTurnStopNotice(); notice != "" {
		t.Fatalf("a recovered subagent left a notice: %q", notice)
	}

	p = &outputLimitProvider{steps: []outputLimitStep{cutOff}}
	ag, st = outputLimitAgent(t, p, nil)
	ag.subagent = &session.SubagentMeta{Name: "general"}
	stop, err = runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonMaxTokens) || p.calls != 1+maxOutputLimitRecoveries {
		t.Fatalf("subagent: stop = %q, err = %v after %d calls; want max_tokens after %d", stop, err, p.calls, 1+maxOutputLimitRecoveries)
	}
	notice := st.TakeTurnStopNotice()
	if !strings.Contains(notice, "subagent") || !strings.Contains(notice, "run it again") || strings.Contains(notice, "Send a message") {
		t.Fatalf("subagent notice = %q", notice)
	}
}

// TestOutputLimitNoticeWithoutRecoveryIsUnchanged pins the notice of a turn that
// ended on a plain cut-off, for a top-level turn and for a subagent.
func TestOutputLimitNoticeWithoutRecoveryIsUnchanged(t *testing.T) {
	top := &session.State{ID: "sess_top"}
	(&Agent{cfg: &config.Config{}, state: top}).noteStopReason("max_tokens", nil, 40)
	if got := top.TakeTurnStopNotice(); got != plainOutputLimitNotice {
		t.Fatalf("top-level notice = %q", got)
	}
	child := &session.State{ID: "sess_child"}
	(&Agent{cfg: &config.Config{}, state: child, subagent: &session.SubagentMeta{Name: "explore"}}).noteStopReason("max_tokens", nil, 8)
	if got := child.TakeTurnStopNotice(); strings.Contains(got, "Send a message") || !strings.Contains(got, "max_tokens") {
		t.Fatalf("subagent notice = %q", got)
	}
}
