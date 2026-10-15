package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A session compacted before blocks existed has one plain summary row. The
// next compaction takes it for the start of the chain: it shows it to the
// summarizer as earlier context, never as transcript to retell, leaves it as it
// was, and adds a block after it.
func TestCompactSessionContinuesARowWrittenBeforeBlocks(t *testing.T) {
	legacy := session.NewCompactionSummaryMessage("LEGACY-SUMMARY", "m")
	st := stateOf(t, []llm.Message{
		userText("question 1"), {Role: llm.RoleAssistant, Content: "answer 1"},
		legacy,
		userText("question 2"), {Role: llm.RoleAssistant, Content: "answer 2"},
		userText("question 3"), {Role: llm.RoleAssistant, Content: "answer 3"},
	})
	keep := 1
	provider := &compactCannedProvider{t: t, summary: "NEW-BLOCK"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)

	res, err := ag.CompactSession(context.Background(), CompactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.CompactedMessages != 2 {
		t.Fatalf("compacted %d messages, want the 2 of the tail before the kept turn", res.CompactedMessages)
	}

	req := provider.requests[0][1].Content
	earlier, transcript, ok := strings.Cut(req, "<transcript>")
	if !ok {
		t.Fatalf("the request has no transcript:\n%s", req)
	}
	if !strings.Contains(earlier, "LEGACY-SUMMARY") || !strings.Contains(earlier, "<earlier-summaries>") {
		t.Fatalf("the earlier row is not shown ahead of the transcript:\n%s", req)
	}
	if strings.Contains(transcript, "LEGACY-SUMMARY") || strings.Contains(transcript, "earlier summary") {
		t.Fatalf("the earlier row is in the transcript to retell:\n%s", req)
	}

	msgs := st.GetMessages()
	if msgs[2].Content != legacy.Content || msgs[2].CompactionBlock {
		t.Fatalf("the earlier row was rewritten: %+v", msgs[2])
	}
	window := windowOf(st)
	if len(window) != 4 || window[0].Content != legacy.Content {
		t.Fatalf("window = %q, want the earlier row, the new block and the kept turn", transcriptText(window))
	}
	block := window[1]
	if !block.CompactionSummary || !block.CompactionBlock ||
		!strings.HasPrefix(block.Content, "The conversation after the summary above was compacted as well.") ||
		!strings.HasSuffix(block.Content, "NEW-BLOCK") {
		t.Fatalf("the new row is not a block that continues the chain: %+v", block)
	}
}

// The first compaction of a session writes a block too, with the preamble of a
// first summary: there is no summary above it to continue.
func TestCompactSessionStartsTheChainWithABlock(t *testing.T) {
	st := seededCompactState(t, 3)
	keep := 1
	provider := &compactCannedProvider{t: t, summary: "FIRST"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)
	if _, err := ag.CompactSession(context.Background(), CompactOptions{}); err != nil {
		t.Fatal(err)
	}
	row := windowOf(st)[0]
	if !row.CompactionBlock || !strings.HasPrefix(row.Content, "The earlier conversation was compacted.") {
		t.Fatalf("first row = %+v, want a block with the plain preamble", row)
	}
	if strings.Contains(provider.requests[0][1].Content, "<earlier-summaries>") {
		t.Fatalf("a first compaction shows earlier summaries:\n%s", provider.requests[0][1].Content)
	}
}

// A /compact with nothing said since the last one has nothing to fold: the
// summaries are not retold again, and the summarizer is not called.
func TestCompactSessionWithNothingAfterTheChainHasNothingToFold(t *testing.T) {
	st := seededCompactState(t, 2)
	keep := 0
	provider := &compactCannedProvider{t: t, summary: "S"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)
	if _, err := ag.CompactSession(context.Background(), CompactOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	_, err := ag.CompactSession(context.Background(), CompactOptions{Force: true})
	if !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("second /compact: err = %v, want ErrNothingToCompact", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("the summarizer was called %d times, want once", len(provider.requests))
	}
}

// The earlier summaries take room in every pass, so the fold's budget is
// what is left of the summarizer's share after them.
func TestCompactionRequestWithoutEarlierSummariesIsUnchanged(t *testing.T) {
	with := compactionRequest("EARLIER", "", "user:\nhi\n", "")[1].Content
	without := compactionRequest("", "", "user:\nhi\n", "")[1].Content
	if !strings.HasSuffix(with, without) {
		t.Fatalf("the earlier summaries are not a section in front of the request as it was:\nwith:    %q\nwithout: %q", with, without)
	}
	if strings.Contains(without, "earlier-summaries") {
		t.Fatalf("a request with no earlier summaries names them: %q", without)
	}
	if compactionInputBudget(100000, 0) <= compactionInputBudget(100000, session.EstimateContextTokens(strings.Repeat("x", 30000))) {
		t.Fatal("the budget does not shrink by the earlier summaries")
	}
}
