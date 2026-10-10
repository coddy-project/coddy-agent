package agent

// Edge cases of the multi-step fold and of the boundary it folds at. The happy
// paths are in features/context_compaction.feature.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
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
	// Room for roughly two of them (a message renders to ~1340 tokens by the
	// estimate the fold budgets with: three ASCII characters to the token).
	chunk := nextCompactionChunk(msgs, 2800)
	if chunk.count != 2 {
		t.Fatalf("chunk covered %d messages, want 2", chunk.count)
	}
	if session.EstimateContextTokens(chunk.body) > 2800 {
		t.Fatalf("chunk body is %d tokens, want it within the 2800 room", session.EstimateContextTokens(chunk.body))
	}
}

// Cyrillic, code and paths cost close to a token a character, which the
// four-characters-per-token estimate this fold used to size its passes with
// reads as a quarter of the cost; a pass built on it is refused whole.
func TestNextCompactionChunkMeasuresDenseTextByTheDenseEstimate(t *testing.T) {
	msgs := make([]llm.Message, 4)
	for i := range msgs {
		msgs[i] = llm.Message{Role: llm.RoleUser, Content: strings.Repeat("п", 2000)}
	}
	chunk := nextCompactionChunk(msgs, 2500)
	if chunk.count != 1 {
		t.Fatalf("chunk covered %d messages, want the one that fits (2000 characters are ~2000 tokens)", chunk.count)
	}
	if got := session.EstimateContextTokens(chunk.body); got > 2500 {
		t.Fatalf("chunk body is %d tokens, want it within the 2500 room", got)
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
	if got := session.EstimateContextTokens(chunk.body); got > compactionMinChunkTokens*5/4 {
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

	res, err := ag.CompactSession(context.Background(), CompactOptions{})
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

	res, err := ag.CompactSession(context.Background(), CompactOptions{Force: true})
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

	res, err := ag.CompactSession(context.Background(), CompactOptions{Force: true})
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

	chain, err := ag.compactionChain("", "")
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

	chain, err := ag.compactionChain("", "")
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

// compactSizeRefusingProvider refuses a request whose transcript (the last
// message) is longer than limit characters, the way a backend with a small
// window does, and words the refusal as refusal(sentTokens) so a test chooses
// what the provider reports.
type compactSizeRefusingProvider struct {
	limit    int
	refusal  func(sentTokens int) error
	summary  string
	requests [][]llm.Message
	refused  int
}

func (p *compactSizeRefusingProvider) Complete(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	p.requests = append(p.requests, append([]llm.Message(nil), messages...))
	// The transcript is the last message; the system prompt before it is the
	// same on every call and no part of what a test varies.
	if len(messages[len(messages)-1].Content) > p.limit {
		p.refused++
		return nil, p.refusal(session.EstimateContextTokens(transcriptText(messages)))
	}
	return &llm.Response{Content: p.summary, StopReason: "end_turn"}, nil
}

func (p *compactSizeRefusingProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
	return nil, fmt.Errorf("Stream must not be called by CompactSession")
}

func longHead(n, chars int) []llm.Message {
	head := make([]llm.Message, n)
	for i := range head {
		head[i] = llm.Message{Role: llm.RoleUser, Content: strings.Repeat("x", chars)}
	}
	return head
}

// A refusal that is not about size fails the same way at half the size, so the
// pass goes to the next model at once instead of spending three calls on
// halving (issue #490).
func TestFoldFailsOverAtOnceOnARefusalThatIsNotAboutSize(t *testing.T) {
	st := seededCompactState(t, 2)
	keep := 1
	broken := &compactCannedProvider{t: t, err: fmt.Errorf("400 Bad Request: Invalid schema for response_format")}
	working := &compactCannedProvider{t: t, summary: "SUMMARY"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, working)

	chain := []compactionCandidate{
		{provider: broken, modelID: "fake/broken"},
		{provider: working, modelID: "fake/model"},
	}
	summary, used, _, err := ag.foldCompactionHead(context.Background(), chain, longHead(4, 2000), "", 100000, nil)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	if summary != "SUMMARY" || used != "fake/model" {
		t.Fatalf("summary %q by %q, want the fallback's", summary, used)
	}
	if len(broken.requests) != 1 {
		t.Fatalf("the broken summarizer was asked %d times, want once", len(broken.requests))
	}
	if len(working.requests) != 1 {
		t.Fatalf("the fallback was asked %d times, want once", len(working.requests))
	}
}

func TestFoldShrinksAPassTheProviderFoundTooLarge(t *testing.T) {
	for _, tc := range []struct {
		name string
		// refusal words the provider's answer from the tokens it was sent.
		refusal      func(sent int) error
		wantRefusals int
		wantCovered  int
	}{
		{
			name:         "no figures in the refusal: halving",
			refusal:      func(int) error { return fmt.Errorf("400 Bad Request: this model's maximum context length is exceeded") },
			wantRefusals: 2, // 8 messages, then 3 (half the tokens), then the one that fits
			wantCovered:  1,
		},
		{
			name: "figures in the refusal: straight under the limit",
			refusal: func(sent int) error {
				return fmt.Errorf("400 Bad Request: This model's maximum context length is %d tokens. However, your messages resulted in %d tokens.", sent/16, sent)
			},
			wantRefusals: 1,
			wantCovered:  1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := seededCompactState(t, 2)
			keep := 1
			provider := &compactSizeRefusingProvider{limit: 4500, refusal: tc.refusal, summary: "SUMMARY"}
			ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)

			rest := longHead(8, 3000)
			chunk := nextCompactionChunk(rest, 100000)
			if chunk.count != len(rest) {
				t.Fatalf("setup: the first pass covers %d of %d messages", chunk.count, len(rest))
			}
			chain := []compactionCandidate{{provider: provider, modelID: "fake/model"}}
			out, _, covered, err := ag.foldOnePass(context.Background(), chain, "", rest, chunk, "")
			if err != nil {
				t.Fatalf("pass: %v", err)
			}
			if out != "SUMMARY" {
				t.Fatalf("summary = %q", out)
			}
			if provider.refused != tc.wantRefusals {
				t.Errorf("the provider refused %d times, want %d", provider.refused, tc.wantRefusals)
			}
			if covered != tc.wantCovered {
				t.Errorf("the pass covered %d messages, want %d", covered, tc.wantCovered)
			}
		})
	}
}

func TestShrunkenPassTokens(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   int
		detail llm.OverflowDetail
		want   int
	}{
		{"no figures: half", 8000, llm.OverflowDetail{}, 4000},
		{"a limit alone: half", 8000, llm.OverflowDetail{Limit: 8000}, 4000},
		{"over by a little: never less than halving", 8000, llm.OverflowDetail{Prompt: 9000, Limit: 8000}, 4000},
		{"far over: aim under the limit", 8000, llm.OverflowDetail{Prompt: 80000, Limit: 8000}, 640},
		{"a prompt under its own limit says nothing about the overflow", 8000, llm.OverflowDetail{Prompt: 1000, Limit: 8000}, 4000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shrunkenPassTokens(tc.body, tc.detail); got != tc.want {
				t.Fatalf("shrunkenPassTokens(%d, %+v) = %d, want %d", tc.body, tc.detail, got, tc.want)
			}
		})
	}
}

// The next report of a refused pass has to be conclusive: both sizes in the log.
func TestFoldLogsTheEstimatedAndTheRefusedSize(t *testing.T) {
	st := seededCompactState(t, 2)
	keep := 1
	provider := &compactSizeRefusingProvider{
		limit: 4500, summary: "SUMMARY",
		refusal: func(sent int) error {
			return fmt.Errorf("404 Not Found: Context limit is 1000 tokens; prompt=%d leaves 0 output tokens", sent)
		},
	}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)
	var logs bytes.Buffer
	ag.log = slog.New(slog.NewTextHandler(&logs, nil))

	chain := []compactionCandidate{{provider: provider, modelID: "fake/model"}}
	if _, _, _, err := ag.foldCompactionHead(context.Background(), chain, longHead(4, 3000), "", 100000, nil); err != nil {
		t.Fatalf("fold: %v", err)
	}
	if provider.refused == 0 {
		t.Fatal("the provider never refused")
	}
	for _, want := range []string{"compaction pass refused as too large", "estimatedTokens=", "providerTokens=", "providerLimit=1000", "retryTokens="} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, logs.String())
		}
	}
}

// The old fold shrank on any refusal; an answer in the wording of a backend
// the classifier has never seen must not leave a session that only /compact
// could save with no way out. A 400 or a 413 that no phrase recognises is cut
// once.
func TestFoldShrinksOnceOnARefusalItCannotRecognise(t *testing.T) {
	for _, status := range []int{400, 413} {
		t.Run(fmt.Sprintf("status %d", status), func(t *testing.T) {
			st := seededCompactState(t, 2)
			keep := 1
			provider := &compactSizeRefusingProvider{
				limit: 4500, summary: "SUMMARY",
				refusal: func(int) error { return apiRefusal(t, status, "") },
			}
			ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)

			rest := longHead(2, 3000)
			chunk := nextCompactionChunk(rest, 100000)
			chain := []compactionCandidate{{provider: provider, modelID: "fake/model"}}
			out, _, covered, err := ag.foldOnePass(context.Background(), chain, "", rest, chunk, "")
			if err != nil {
				t.Fatalf("a single-model chain did not recover after one cut: %v", err)
			}
			if out != "SUMMARY" || covered != 1 {
				t.Fatalf("summary %q covering %d messages, want SUMMARY covering the 1 that fits", out, covered)
			}
			if provider.refused != 1 {
				t.Fatalf("the provider refused %d times, want once", provider.refused)
			}
		})
	}
}

func TestFoldFailsOverAfterOneCutWhenAnUnrecognisedBadRequestPersists(t *testing.T) {
	st := seededCompactState(t, 2)
	keep := 1
	broken := &compactCannedProvider{t: t, err: apiRefusal(t, 400, "")}
	working := &compactCannedProvider{t: t, summary: "SUMMARY"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, working)

	rest := longHead(4, 2000)
	chunk := nextCompactionChunk(rest, 100000)
	chain := []compactionCandidate{
		{provider: broken, modelID: "fake/broken"},
		{provider: working, modelID: "fake/model"},
	}
	out, used, _, err := ag.foldOnePass(context.Background(), chain, "", rest, chunk, "")
	if err != nil || out != "SUMMARY" || used != "fake/model" {
		t.Fatalf("pass = %q by %q, %v; want the fallback's summary", out, used, err)
	}
	if len(broken.requests) != 2 {
		t.Fatalf("the refusing summarizer was asked %d times, want the pass and one cut of it", len(broken.requests))
	}
	if len(working.requests) != 1 {
		t.Fatalf("the fallback was asked %d times, want once", len(working.requests))
	}
}

func TestFoldDoesNotCutForAStatusThatIsNotAboutSize(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500, 502} {
		t.Run(fmt.Sprintf("status %d", status), func(t *testing.T) {
			st := seededCompactState(t, 2)
			keep := 1
			broken := &compactCannedProvider{t: t, err: apiRefusal(t, status, "")}
			working := &compactCannedProvider{t: t, summary: "SUMMARY"}
			ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, working)

			rest := longHead(4, 2000)
			chunk := nextCompactionChunk(rest, 100000)
			chain := []compactionCandidate{
				{provider: broken, modelID: "fake/broken"},
				{provider: working, modelID: "fake/model"},
			}
			if _, _, _, err := ag.foldOnePass(context.Background(), chain, "", rest, chunk, ""); err != nil {
				t.Fatal(err)
			}
			if len(broken.requests) != 1 {
				t.Fatalf("a %d was asked %d times, want once", status, len(broken.requests))
			}
		})
	}
}

// A refusal can say anything. Figures no window has must not reach arithmetic
// that wraps (2^62 * 100 is 0 in 64 bits).
func TestShrunkenPassTokensSurvivesAbsurdFigures(t *testing.T) {
	text := errors.New("400: prompt is too long: 4611686018427387904 tokens > 1 maximum")
	if got := shrunkenPassTokens(8000, llm.OverflowDetailOf(text)); got != 4000 {
		t.Fatalf("shrunkenPassTokens on the figures of %q = %d, want halving", text, got)
	}
	for _, d := range []llm.OverflowDetail{
		{Prompt: 4611686018427387904, Limit: 1},
		{Prompt: math.MaxInt, Limit: math.MaxInt - 1},
		{Prompt: math.MaxInt, Limit: 1},
	} {
		if got := shrunkenPassTokens(8000, d); got < 0 || got > 4000 {
			t.Errorf("shrunkenPassTokens(8000, %+v) = %d, want a figure between 0 and the half", d, got)
		}
	}
}

func TestElideToTokensCutsEvenWhenTheCorrectionRoundsToNothing(t *testing.T) {
	long := strings.Repeat("abcdefghij", 1000)
	for _, tokens := range []int{1, 2, 5} {
		out := elideToTokens(long, tokens)
		if len(out) >= len(long) {
			t.Errorf("elideToTokens(%d chars, %d) returned %d chars: the entry went through uncut", len(long), tokens, len(out))
		}
		if !strings.Contains(out, "characters omitted") {
			t.Errorf("elideToTokens(%d) does not say what it cut: %q", tokens, out)
		}
	}
}
