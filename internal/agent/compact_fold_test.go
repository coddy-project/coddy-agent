package agent

// Edge cases of the multi-step fold and of the boundary it folds at. The happy
// paths are in features/context_compaction.feature.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestCompactionInputBudgetFloorsOnATinyWindow(t *testing.T) {
	for _, tc := range []struct {
		name         string
		window       int
		instructions int
		want         int
	}{
		{name: "unknown window", window: 0, want: compactionMinChunkTokens},
		{name: "window smaller than the system prompt", window: 100, want: compactionMinChunkTokens},
		{name: "instructions eat the window", window: 4000, instructions: 4000, want: compactionMinChunkTokens},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := compactionInputBudget(tc.window, tc.instructions); got != tc.want {
				t.Fatalf("budget = %d, want %d", got, tc.want)
			}
		})
	}
	// A real window leaves room for a real chunk.
	if got := compactionInputBudget(262144, 0); got <= compactionMinChunkTokens {
		t.Fatalf("budget on a 262144 window = %d, want more than the floor", got)
	}
}

func TestNextCompactionChunkStopsAtTheBudget(t *testing.T) {
	msgs := make([]llm.Message, 4)
	for i := range msgs {
		msgs[i] = llm.Message{Role: llm.RoleUser, Content: strings.Repeat("x", 4000)}
	}
	// Room for roughly two of them (a message renders to ~1000 tokens).
	chunk := nextCompactionChunk(msgs, 2200)
	if chunk.count != 2 {
		t.Fatalf("chunk covered %d messages, want 2", chunk.count)
	}
	if session.EstimateTokens(chunk.body) > 2400 {
		t.Fatalf("chunk body is %d tokens, want it near the 2200 room", session.EstimateTokens(chunk.body))
	}
}

func TestNextCompactionChunkElidesOneMessageThatCannotFit(t *testing.T) {
	huge := llm.Message{Role: llm.RoleUser, Content: "HEAD" + strings.Repeat("y", 200000) + "TAIL"}
	chunk := nextCompactionChunk([]llm.Message{huge, {Role: llm.RoleUser, Content: "next"}}, compactionMinChunkTokens)
	// The fold must make progress even on a single message bigger than a pass.
	if chunk.count != 1 {
		t.Fatalf("chunk covered %d messages, want the one oversized message", chunk.count)
	}
	if !strings.Contains(chunk.body, "characters omitted") {
		t.Fatal("oversized message was not elided")
	}
	if !strings.Contains(chunk.body, "HEAD") || !strings.Contains(chunk.body, "TAIL") {
		t.Fatal("elision dropped the head or the tail of the message")
	}
	if got := session.EstimateTokens(chunk.body); got > compactionMinChunkTokens*2 {
		t.Fatalf("elided body is %d tokens, want it near the %d room", got, compactionMinChunkTokens)
	}
}

func TestNextCompactionChunkOnAnEmptyHead(t *testing.T) {
	if chunk := nextCompactionChunk(nil, 1000); chunk.count != 0 || chunk.body != "" {
		t.Fatalf("empty head gave %+v", chunk)
	}
}

// compactShrinkingProvider refuses any request longer than limit, the way a
// provider answers when the history does not fit its context window.
type compactShrinkingProvider struct {
	t        *testing.T
	limit    int
	summary  string
	requests [][]llm.Message
	refusals int
}

func (p *compactShrinkingProvider) Complete(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	p.requests = append(p.requests, append([]llm.Message(nil), messages...))
	size := 0
	for _, m := range messages {
		size += len(m.Content)
	}
	if size > p.limit {
		p.refusals++
		return nil, fmt.Errorf("400 Bad Request: this model's maximum context length is exceeded, reduce the length of the messages")
	}
	return &llm.Response{Content: p.summary, StopReason: "end_turn"}, nil
}

func (p *compactShrinkingProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
	p.t.Fatal("Stream must not be called by CompactSession")
	return nil, nil
}

func TestFoldRetriesASmallerPassWhenTheProviderRefuses(t *testing.T) {
	st := seededCompactState(t, 2)
	keep := 1
	provider := &compactShrinkingProvider{t: t, limit: 3000, summary: "SUMMARY"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)

	head := []llm.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("a", 2000)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("b", 2000)},
	}
	// A budget that says both messages fit; the provider says otherwise, so the
	// pass has to come down on its own.
	summary, steps, err := ag.foldCompactionHead(context.Background(), provider, head, "", 100000, nil)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	if provider.refusals == 0 {
		t.Fatal("the provider never refused, so nothing was retried")
	}
	if summary != "SUMMARY" {
		t.Fatalf("summary = %q", summary)
	}
	if steps < 2 {
		t.Fatalf("steps = %d, want the fold to have taken more than one pass", steps)
	}
}

func TestFoldGivesUpOnAnErrorThatShrinkingCannotFix(t *testing.T) {
	st := seededCompactState(t, 2)
	keep := 1
	provider := &compactCannedProvider{t: t, err: fmt.Errorf("401 unauthorized")}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)

	head := []llm.Message{{Role: llm.RoleUser, Content: "short"}}
	_, _, err := ag.foldCompactionHead(context.Background(), provider, head, "", 100000, nil)
	if err == nil {
		t.Fatal("expected the failure to surface")
	}
	if !strings.Contains(err.Error(), "401 unauthorized") {
		t.Fatalf("error = %v, want the provider's own message", err)
	}
	// One message already at the floor cannot be halved, so the call is made
	// once rather than three more times on the same input.
	if len(provider.requests) != 1 {
		t.Fatalf("provider called %d times, want 1", len(provider.requests))
	}
}

func TestPlannedCompactionStepsCountsTheWholeHead(t *testing.T) {
	head := make([]llm.Message, 10)
	for i := range head {
		head[i] = llm.Message{Role: llm.RoleUser, Content: strings.Repeat("x", 4000)}
	}
	if got := plannedCompactionSteps(head, 1000); got < 10 {
		t.Fatalf("planned steps = %d, want at least one per message", got)
	}
	if got := plannedCompactionSteps(head, 1000000); got != 1 {
		t.Fatalf("planned steps on a large budget = %d, want 1", got)
	}
	if got := plannedCompactionSteps(nil, 0); got != 1 {
		t.Fatalf("planned steps with no budget = %d, want 1", got)
	}
}

func TestElideMiddleKeepsBothEnds(t *testing.T) {
	s := "START" + strings.Repeat("m", 1000) + "END"
	got := elideMiddle(s, 100)
	if !strings.HasPrefix(got, "START") || !strings.HasSuffix(got, "END") {
		t.Fatalf("elided text lost an end: %q", got)
	}
	if !strings.Contains(got, "characters omitted") {
		t.Fatalf("elided text does not say what went: %q", got)
	}
	if short := elideMiddle("short", 100); short != "short" {
		t.Fatalf("a string within the limit was changed: %q", short)
	}
}

// Astra's finding on the branch: the floor has to hold for the configured
// keep_recent_turns too, not only for the retries below it.
func TestAutoCompactionNeverFoldsThePromptWithKeepRecentTurnsZero(t *testing.T) {
	st := seededCompactState(t, 3)
	zero := 0
	provider := &compactCannedProvider{t: t, summary: "SUMMARY"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &zero}, provider)

	res, err := ag.CompactSession(context.Background(), "", false)
	if err != nil {
		t.Fatalf("auto compaction: %v", err)
	}
	if res.KeptMessages == 0 {
		t.Fatal("automatic compaction folded the whole window, prompt included")
	}
	msgs := session.MessagesForLLM(st.GetMessages())
	last := msgs[len(msgs)-1]
	if strings.Contains(last.Content, "SUMMARY") {
		t.Fatal("the summary is the last thing the model would see: the prompt was folded")
	}
	if !strings.Contains(transcriptText(msgs), "question 3") {
		t.Fatalf("the prompt being answered is gone from the replayed window: %q", transcriptText(msgs))
	}
}

func TestManualCompactionStillFoldsEverythingWithKeepRecentTurnsZero(t *testing.T) {
	st := seededCompactState(t, 3)
	zero := 0
	provider := &compactCannedProvider{t: t, summary: "SUMMARY"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &zero}, provider)

	res, err := ag.CompactSession(context.Background(), "", true)
	if err != nil {
		t.Fatalf("manual compaction: %v", err)
	}
	if res.KeptMessages != 0 {
		t.Fatalf("kept %d messages, want /compact with keep_recent_turns 0 to fold everything", res.KeptMessages)
	}
}
