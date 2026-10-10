package agent

// How the recovery of a step the output limit cut off (output_limit_test.go)
// meets a request the provider refuses as larger than the model's window
// (context_overflow.go, compact_fold.go). The two work on different things -
// the recovery on a response that came back empty, the overflow handling on an
// error or on the summarizer's own calls - and these tests hold that they do
// not step on each other.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

const mlxContextLimitRefusal = `provider "mlx" (http://192.0.2.10:8080/v1): openai stream: POST "http://192.0.2.10:8080/v1/chat/completions": 404 Not Found "Context limit is 49152 tokens; prompt=49182 leaves 0 output tokens, below the minimum 16"`

func withWindow(tokens int) func(*config.Config) {
	return func(c *config.Config) { c.Models[0].MaxContextTokens = tokens }
}

// The nudge that asks for a shorter step can push the request past the window.
// The refusal that answers it ends the turn on the honest message: it is not an
// empty answer to ask again, not a failure of the provider's lane to run after a
// pause, and the notice of an output-limit stop is not written over it.
func TestOutputLimitRecoveryRefusedForItsSizeEndsOnTheOverflowMessage(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  func(t *testing.T) error
	}{
		{"worded refusal of a local backend", func(*testing.T) error { return errors.New(mlxContextLimitRefusal) }},
		{"typed code in a server error", func(t *testing.T) error {
			return apiRefusal(t, http.StatusInternalServerError, `{"message":"refused","type":"invalid_request_error","code":"context_length_exceeded"}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &outputLimitProvider{steps: []outputLimitStep{cutOff, {err: tc.err(t)}, answered}}
			ag, st := outputLimitAgent(t, p, withWindow(49152))

			stop, err := runOutputLimitTurn(ag)
			if err == nil || stop != string(acp.StopReasonRefused) {
				t.Fatalf("stop = %q, err = %v; want the turn refused with the overflow message", stop, err)
			}
			if !strings.HasPrefix(err.Error(), "context window exceeded") || !llm.IsContextOverflow(err) {
				t.Fatalf("not the overflow message: %v", err)
			}
			if p.calls != 2 {
				t.Fatalf("provider asked %d times, want 2: the cut-off step and the one refusal, nothing run again", p.calls)
			}
			if got := countNudges(p.seen[1], outputLimitNudge); got != 1 {
				t.Fatalf("the refused request carried the output-limit nudge %d times, want 1 (the test must be in the recovery)", got)
			}
			if notice := st.TakeTurnStopNotice(); notice != "" {
				t.Fatalf("an output-limit notice was written over the overflow message: %q", notice)
			}
			if ag.outputLimitRecoveriesSpent != 0 {
				t.Fatalf("outputLimitRecoveriesSpent = %d on a turn that did not end on the output limit", ag.outputLimitRecoveriesSpent)
			}
			for _, m := range st.GetMessages() {
				if m.Role == llm.RoleUser && m.Content == outputLimitNudge {
					t.Fatal("the local-only nudge reached the transcript")
				}
			}

			// The next turn starts clean: no leftover nudge, and the cut-off step
			// that stayed in the transcript is not replayed.
			stop, err = ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "go on"}})
			if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 3 {
				t.Fatalf("next turn: stop = %q, err = %v after %d calls; want end_turn after 3", stop, err, p.calls)
			}
			if got := countNudges(p.seen[2], outputLimitNudge); got != 0 {
				t.Fatalf("the next turn carries a nudge of the one that ended: %d", got)
			}
			if hasEmptyAssistant(p.seen[2]) {
				t.Fatal("the next turn replays the step the output limit cut off")
			}
		})
	}
}

// foldRefusingProvider answers the summarizer's first calls with err, as a
// backend whose window is smaller than the one it was configured with does, and
// every later call, and the loop's own, as outputLimitProvider does.
type foldRefusingProvider struct {
	*outputLimitProvider
	err           error
	refusals      int
	completeCalls int
	completeSizes []int
}

func (p *foldRefusingProvider) Complete(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	p.completeCalls++
	p.completeSizes = append(p.completeSizes, estimateMessagesTokens(msgs))
	if p.completeCalls <= p.refusals {
		return nil, p.err
	}
	return p.outputLimitProvider.Complete(ctx, msgs, tools)
}

// A compaction between the cut-off step and its retry folds the history on the
// summarizer's own calls. When the backend refuses the first of them for its
// size, the fold cuts the pass and asks again (compact_fold.go), and the retry
// of the cut-off step still goes out as the request that failed: the summary in
// place of the folded head, the nudge once, the empty step nowhere.
func TestOutputLimitRecoverySurvivesACompactionWhoseFirstPassIsRefused(t *testing.T) {
	enabled, keep := false, 1
	base := &outputLimitProvider{}
	base.steps = []outputLimitStep{
		{reasoning: cutOff.reasoning, stop: "max_tokens", during: func() { enabled = true }},
		answered,
	}
	p := &foldRefusingProvider{outputLimitProvider: base, err: errors.New(mlxContextLimitRefusal), refusals: 1}
	ag, st := outputLimitAgent(t, p, func(c *config.Config) {
		c.Compaction = config.Compaction{Enabled: &enabled, ThresholdPercent: 1, KeepRecentTurns: &keep}
		c.Models[0].MaxContextTokens = 10000
	})
	for i := 0; i < 4; i++ {
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "Earlier question about the module " + strings.Repeat("alpha ", 40)})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Earlier answer " + strings.Repeat("beta ", 40)})
	}

	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) || base.calls != 2 {
		t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 2", stop, err, base.calls)
	}
	if p.completeCalls < 2 {
		t.Fatalf("the summarizer was asked %d times; the test needs a refused pass and its retry", p.completeCalls)
	}
	if got := countNudges(base.seen[1], outputLimitNudge); got != 1 {
		t.Fatalf("the request after the compaction carries the nudge %d times, want 1", got)
	}
	if hasEmptyAssistant(base.seen[1]) {
		t.Fatal("the compaction resurrected the cut-off step")
	}
	var summarized bool
	for _, m := range base.seen[1] {
		if strings.Contains(m.Content, "Earlier turn summary.") {
			summarized = true
		}
	}
	if !summarized {
		t.Fatal("the history was not compacted between the two requests; the test proves nothing")
	}
	if notice := st.TakeTurnStopNotice(); notice != "" {
		t.Fatalf("a recovered turn left a notice: %q", notice)
	}
	// The retry of the refused pass was smaller than the pass that was refused.
	if p.completeSizes[1] >= p.completeSizes[0] {
		t.Fatalf("summarizer request sizes %v: the retry did not send less", p.completeSizes)
	}
}

// The backend that refuses every pass leaves the compaction failed; the loop
// carries on with the history it has, and the cut-off step is still asked again.
func TestOutputLimitRecoveryGoesOnWhenCompactionCannotFold(t *testing.T) {
	enabled, keep := false, 1
	base := &outputLimitProvider{}
	base.steps = []outputLimitStep{
		{reasoning: cutOff.reasoning, stop: "max_tokens", during: func() { enabled = true }},
		answered,
	}
	p := &foldRefusingProvider{outputLimitProvider: base, err: errors.New(mlxContextLimitRefusal), refusals: 1 << 20}
	ag, st := outputLimitAgent(t, p, func(c *config.Config) {
		c.Compaction = config.Compaction{Enabled: &enabled, ThresholdPercent: 1, KeepRecentTurns: &keep}
		c.Models[0].MaxContextTokens = 10000
	})
	for i := 0; i < 4; i++ {
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "Earlier question about the module " + strings.Repeat("alpha ", 40)})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Earlier answer " + strings.Repeat("beta ", 40)})
	}

	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) || base.calls != 2 {
		t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 2", stop, err, base.calls)
	}
	if got := countNudges(base.seen[1], outputLimitNudge); got != 1 {
		t.Fatalf("the request carries the nudge %d times, want 1", got)
	}
	if hasEmptyAssistant(base.seen[1]) {
		t.Fatal("the cut-off step was replayed")
	}
}
