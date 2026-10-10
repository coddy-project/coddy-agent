package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

var errConnectionReset = errors.New("read: connection reset by peer")

// recoveringAgent is outputLimitAgent with the pause before a provider recovery
// cut to a few milliseconds.
func recoveringAgent(t *testing.T, p llm.Provider) (*Agent, *session.State) {
	t.Helper()
	return outputLimitAgent(t, p, func(c *config.Config) { c.Agent.LLMRetryBaseMS = 1 })
}

// TestPromptCacheProviderRecoveryKeepsThePendingRecoveries: a provider recovery
// rebuilds the history from the transcript, which still holds the steps that
// came back empty and none of the local-only nudges. The request run again must
// be the one that failed - the empty steps left out, the nudges the recovery has
// earned back at the end - not a history that ends on the cut-off step.
func TestPromptCacheProviderRecoveryKeepsThePendingRecoveries(t *testing.T) {
	signedEmpty := outputLimitStep{reasoning: "Hmm.", signature: "sig", stop: "end_turn"}
	unsignedEmpty := outputLimitStep{reasoning: "Hmm.", stop: "end_turn"}
	failing := outputLimitStep{err: errConnectionReset}
	for _, tc := range []struct {
		name   string
		steps  []outputLimitStep
		nudges map[string]int // text -> how many times the failed request carried it
	}{
		{
			// Unsigned thinking is what Anthropic cannot replay as the final turn.
			name:   "output limit",
			steps:  []outputLimitStep{cutOff, failing, answered},
			nudges: map[string]int{outputLimitNudge: 1},
		},
		{
			name:   "output limit with signed thinking",
			steps:  []outputLimitStep{{reasoning: "Thinking.", signature: "sig", stop: "max_tokens"}, failing, answered},
			nudges: map[string]int{outputLimitNudge: 1},
		},
		{
			name:   "output limit twice",
			steps:  []outputLimitStep{cutOff, cutOff, failing, answered},
			nudges: map[string]int{outputLimitNudge: 2},
		},
		{
			name:   "empty re-issue",
			steps:  []outputLimitStep{signedEmpty, failing, answered},
			nudges: map[string]int{},
		},
		{
			name:   "empty nudge",
			steps:  []outputLimitStep{signedEmpty, signedEmpty, failing, answered},
			nudges: map[string]int{emptyAssistantContinuationNudge: 1},
		},
		{
			name:   "output limit then empty nudge",
			steps:  []outputLimitStep{cutOff, unsignedEmpty, unsignedEmpty, failing, answered},
			nudges: map[string]int{outputLimitNudge: 1, emptyAssistantContinuationNudge: 1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &outputLimitProvider{steps: tc.steps}
			ag, st := recoveringAgent(t, p)
			stop, err := runOutputLimitTurn(ag)
			if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != len(tc.steps) {
				t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after %d", stop, err, p.calls, len(tc.steps))
			}
			failed, rerun := p.seen[len(p.seen)-2], p.seen[len(p.seen)-1]
			if got, want := strings.Join(requestHistory(rerun), "\n"), strings.Join(requestHistory(failed), "\n"); got != want {
				t.Fatalf("the request run again is not the one that failed:\nfailed: %s\nagain:  %s", want, got)
			}
			if hasEmptyAssistant(rerun) {
				t.Fatalf("the run-again request ends on a step that came back empty: %+v", rerun)
			}
			for text, want := range tc.nudges {
				if got := countNudges(rerun, text); got != want {
					t.Fatalf("the run-again request carries %q %d times, want %d", text, got, want)
				}
			}
			if notice := st.TakeTurnStopNotice(); notice != "" {
				t.Fatalf("a recovered turn left a stop notice: %q", notice)
			}
		})
	}
}

// TestProviderRecoveryKeepsThePendingNudgesBeforeTheKeptAnswer: when the failed
// call had already streamed text, the transcript holds it and the rebuilt
// history ends with it; the nudges the step earned stay where they were sent,
// before the answer they led to, and the continue message follows.
func TestProviderRecoveryKeepsThePendingNudgesBeforeTheKeptAnswer(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{cutOff, {text: "The module starts with", err: errConnectionReset}, answered}}
	ag, _ := recoveringAgent(t, p)
	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 3 {
		t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 3", stop, err, p.calls)
	}
	hist := requestHistory(p.seen[2])
	nudgeAt, keptAt, continueAt := -1, -1, -1
	for i, line := range hist {
		switch {
		case strings.HasSuffix(line, outputLimitNudge):
			nudgeAt = i
		case strings.HasSuffix(line, "The module starts with"):
			keptAt = i
		case strings.HasSuffix(line, providerRecoveryNudge):
			continueAt = i
		}
	}
	if nudgeAt < 0 || keptAt != nudgeAt+1 || continueAt != keptAt+1 || continueAt != len(hist)-1 {
		t.Fatalf("want the nudge, the kept answer and the continue message in that order at the end, got %q", hist)
	}
	if hasEmptyAssistant(p.seen[2]) {
		t.Fatalf("the cut-off step is in the rebuilt history: %+v", p.seen[2])
	}
}
