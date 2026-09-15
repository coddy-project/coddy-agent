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
	chain := []compactionCandidate{{provider: provider, modelID: "fake/model"}}
	summary, _, steps, err := ag.foldCompactionHead(context.Background(), chain, head, "", 100000, nil)
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
	chain := []compactionCandidate{{provider: provider, modelID: "fake/model"}}
	_, _, _, err := ag.foldCompactionHead(context.Background(), chain, head, "", 100000, nil)
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

// Issue #273: a session fills its window by repeating itself, and the repeats
// are what push the fold past the summarizer's window.
func TestDedupeCompactionHeadDropsRepeatedLongLines(t *testing.T) {
	logLine := "go: downloading github.com/example/module v1.2.3 checksum verified ok"
	build := strings.Repeat(logLine+"\n", 50)
	head := []llm.Message{
		{Role: llm.RoleUser, Content: "here is the build log:\n" + build},
		{Role: llm.RoleAssistant, Content: "and again:\n" + build},
	}
	out, dropped := dedupeCompactionHead(head)
	if dropped < 90 {
		t.Fatalf("dropped %d repeated lines, want nearly all 99 copies", dropped)
	}
	joined := out[0].Content + out[1].Content
	if strings.Count(joined, logLine) != 1 {
		t.Fatalf("the line survives %d times, want exactly one copy", strings.Count(joined, logLine))
	}
	if !strings.Contains(out[1].Content, "repeated line(s) removed") {
		t.Fatal("the thinned entry does not say that repeats were removed")
	}
	if !strings.Contains(out[0].Content, "here is the build log:") ||
		!strings.Contains(out[1].Content, "and again:") {
		t.Fatal("dedup dropped a line that appeared only once")
	}
	if session.EstimateTokens(joined) >= session.EstimateTokens(head[0].Content+head[1].Content) {
		t.Fatal("dedup did not make the head smaller")
	}
}

func TestDedupeCompactionHeadKeepsShortStructuralLines(t *testing.T) {
	code := "func main() {\n\tif err != nil {\n\t\treturn err\n\t}\n}\n"
	head := []llm.Message{
		{Role: llm.RoleAssistant, Content: code},
		{Role: llm.RoleAssistant, Content: code},
	}
	out, dropped := dedupeCompactionHead(head)
	if dropped != 0 {
		t.Fatalf("dropped %d short lines; braces and returns are structure, not repetition", dropped)
	}
	if out[1].Content != code {
		t.Fatalf("second copy was rewritten: %q", out[1].Content)
	}
}

func TestDedupeCompactionHeadLeavesAHeadWithoutRepeatsAlone(t *testing.T) {
	head := []llm.Message{
		{Role: llm.RoleUser, Content: "a question long enough to be considered for deduplication"},
		{Role: llm.RoleAssistant, Content: "an answer long enough to be considered for deduplication"},
	}
	out, dropped := dedupeCompactionHead(head)
	if dropped != 0 {
		t.Fatalf("dropped %d lines from a head with no repeats", dropped)
	}
	for i := range head {
		if out[i].Content != head[i].Content {
			t.Fatalf("message %d was rewritten: %q", i, out[i].Content)
		}
	}
}

// Issue #247: a summarizer that refuses must not be the end of a compaction,
// because a session out of room has nothing else left.
func TestCompactionFallsBackToTheNextSummarizer(t *testing.T) {
	st := seededCompactState(t, 3)
	keep := 1
	broken := &compactCannedProvider{t: t, err: fmt.Errorf("503 model overloaded")}
	working := &compactCannedProvider{t: t, summary: "SUMMARY FROM THE DEPUTY"}

	ag := compactTestAgent(t, st, config.Compaction{
		KeepRecentTurns: &keep,
		Model:           "fake/broken",
		FallbackModels:  []string{"fake/model"},
	}, nil)
	ag.cfg.Models = append(ag.cfg.Models, config.ModelEntry{Model: "fake/broken", MaxTokens: 100})
	ag.providerFactory = func(in llm.ProviderInput) (llm.Provider, error) {
		if strings.Contains(in.Model, "broken") {
			return broken, nil
		}
		return working, nil
	}

	res, err := ag.CompactSession(context.Background(), "", true)
	if err != nil {
		t.Fatalf("compaction: %v", err)
	}
	if len(broken.requests) == 0 {
		t.Fatal("the configured summarizer was never tried")
	}
	if res.Model != "fake/model" {
		t.Fatalf("summary model = %q, want the fallback that answered", res.Model)
	}
	if !strings.Contains(res.Summary, "DEPUTY") {
		t.Fatalf("summary = %q, want the fallback's answer", res.Summary)
	}
}

func TestCompactionChainEndsAtTheSessionModel(t *testing.T) {
	st := seededCompactState(t, 2)
	keep := 1
	ag := compactTestAgent(t, st, config.Compaction{
		KeepRecentTurns: &keep,
		// Neither names a configured model, so only the session's own is left.
		Model:          "fake/missing",
		FallbackModels: []string{"fake/also-missing"},
	}, &compactCannedProvider{t: t, summary: "SUMMARY"})

	chain, err := ag.compactionChain()
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	if len(chain) != 1 || chain[0].modelID != "fake/model" {
		t.Fatalf("chain = %+v, want only the session's model", chain)
	}
}

func TestCompactionChainIsOrderedAndDeduplicated(t *testing.T) {
	st := seededCompactState(t, 2)
	keep := 1
	ag := compactTestAgent(t, st, config.Compaction{
		KeepRecentTurns: &keep,
		Model:           "fake/second",
		// The session model repeated in the list must not be tried twice.
		FallbackModels: []string{"fake/model", "fake/second"},
	}, &compactCannedProvider{t: t, summary: "SUMMARY"})
	ag.cfg.Models = append(ag.cfg.Models, config.ModelEntry{Model: "fake/second", MaxTokens: 100})

	chain, err := ag.compactionChain()
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	var ids []string
	for _, c := range chain {
		ids = append(ids, c.modelID)
	}
	if len(ids) != 2 || ids[0] != "fake/second" || ids[1] != "fake/model" {
		t.Fatalf("chain = %v, want [fake/second fake/model]", ids)
	}
}
