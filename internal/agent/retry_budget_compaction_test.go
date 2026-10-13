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

type retryCompactionProvider struct {
	compactEnabled   *bool
	compactAfter     int
	calls, summaries int
	seen             [][]llm.Message
}

func (p *retryCompactionProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	p.summaries++
	return &llm.Response{Content: "Earlier turn summary."}, nil
}

func (p *retryCompactionProvider) Stream(_ context.Context, msgs []llm.Message, _ []llm.ToolDefinition, emit func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	p.seen = append(p.seen, append([]llm.Message(nil), msgs...))
	if p.calls == p.compactAfter {
		*p.compactEnabled = true
	}
	if p.calls <= 2 {
		emit(llm.StreamChunk{ReasoningDelta: "signed reasoning"})
		return &llm.Response{Reasoning: "signed reasoning", ReasoningSignature: "signature", StopReason: "end_turn"}, nil
	}
	emit(llm.StreamChunk{TextDelta: "An answer."})
	return &llm.Response{Content: "An answer.", StopReason: "end_turn"}, nil
}

func TestReActRetryBudgetRecoverySurvivesCompaction(t *testing.T) {
	for _, after := range []int{1, 2} {
		t.Run(map[int]string{1: "reissue", 2: "nudge"}[after], func(t *testing.T) {
			two, keep, enabled := 2, 1, false
			f := newRetryBudgetFixture(t, &two, 10, "answer")
			f.st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "Earlier question."})
			f.st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Earlier answer."})
			f.ag.cfg.Compaction = config.Compaction{Enabled: &enabled, ThresholdPercent: 1, KeepRecentTurns: &keep}
			f.ag.cfg.Models[0].MaxContextTokens = 10000
			p := &retryCompactionProvider{compactEnabled: &enabled, compactAfter: after}
			f.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
				return llm.WrapResilient(p, llm.ResilientOptions{RetryMax: two}), nil
			}
			f.run()
			if p.calls != 3 || f.err != nil || f.stop != string(acp.StopReasonEndTurn) {
				t.Fatalf("summaries=%d calls=%d stop=%s err=%v", p.summaries, p.calls, f.stop, f.err)
			}
			// The threshold of 1% puts every step over it: the earlier turn is
			// folded once, and the turn's own earlier steps may then be folded
			// behind its prompt, once - that fold cannot get under 1%, so the
			// turn makes no further one (inTurnFoldSpent).
			var rows []llm.Message
			for _, msg := range f.st.GetMessages() {
				if msg.CompactionSummary {
					rows = append(rows, msg)
				}
			}
			if len(rows) != p.summaries || len(rows) < 1 || len(rows) > 2 {
				t.Fatalf("summaries=%d rows=%d: want the earlier turn folded once and at most one fold of the turn itself", p.summaries, len(rows))
			}
			if _, _, inTurn := session.SplitInTurnSummary(rows[0].Content); inTurn {
				t.Fatalf("the first compaction must fold the earlier turn, not the turn in progress: %q", rows[0].Content)
			}
			if len(rows) == 2 {
				if _, _, inTurn := session.SplitInTurnSummary(rows[1].Content); !inTurn {
					t.Fatalf("a second compaction must be the fold of the turn in progress: %q", rows[1].Content)
				}
			}
			for call, msgs := range p.seen {
				var nudges int
				for _, msg := range msgs {
					if msg.Role == llm.RoleAssistant && strings.TrimSpace(msg.Content) == "" && len(msg.ToolCalls) == 0 {
						t.Errorf("call %d carries empty assistant after compaction", call+1)
					}
					if msg.Role == llm.RoleUser && msg.Content == emptyAssistantContinuationNudge {
						nudges++
					}
				}
				wantNudges := 0
				if call == 2 {
					wantNudges = 1
				}
				if nudges != wantNudges {
					t.Errorf("call %d has %d nudges, want %d", call+1, nudges, wantNudges)
				}
			}
			var signatures int
			for _, msg := range f.st.GetMessages() {
				if msg.ReasoningSignature == "signature" {
					signatures++
				}
			}
			if signatures != 2 {
				t.Errorf("kept signed reasoning messages=%d, want 2", signatures)
			}
		})
	}
}
