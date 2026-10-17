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

// A /compact with nothing said since the last one has nothing to fold. The
// command and its answer are the only rows after the chain, and folding them
// would retell the chain (before blocks) or add a block that summarizes a
// command: the summarizer is not called and the chain stays as it is.
func TestCompactCommandWithNothingSaidSinceTheLastOneHasNothingToFold(t *testing.T) {
	st := seededCompactState(t, 2)
	keep := 0
	provider := &compactCannedProvider{t: t, summary: "S"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)
	for range 2 {
		if _, err := ag.runCompactCommand(context.Background(), compactCommandArgs{}, "/compact"); err != nil {
			t.Fatal(err)
		}
	}
	if len(provider.requests) != 1 {
		t.Fatalf("the summarizer was called %d times, want once:\n%s", len(provider.requests), transcriptText(st.GetMessages()))
	}
	if rows := summaryRowsOf(st); rows != 1 {
		t.Fatalf("the transcript holds %d summary rows, want the first one only", rows)
	}
	msgs := st.GetMessages()
	if last := msgs[len(msgs)-1].Content; !strings.HasPrefix(last, "Nothing to compact") {
		t.Fatalf("the second /compact answered %q, want nothing to compact", last)
	}
	// The same holds for the route and the tool, which write no command rows.
	if _, err := ag.CompactSession(context.Background(), CompactOptions{Force: true}); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("a compaction after two /compact: err = %v, want ErrNothingToCompact", err)
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

// The rows of the chain are part of every request after an in-turn fold, so
// the fold's budget for the steps it keeps is what its aim leaves after them.
// Without that, a chain near its share of the window would put the request
// after the fold far past the aim, and the turn would fold again within a few
// steps.
func TestInTurnFoldBudgetLeavesRoomForTheChain(t *testing.T) {
	off := false
	comp := config.Compaction{ResultEviction: config.ResultEviction{Enabled: &off}}
	earlier := []llm.Message{
		userText("an earlier request"), {Role: llm.RoleAssistant, Content: "done"},
		session.NewCompactionBlockMessage("EARLIER "+strings.Repeat("x", 4000*3), "m", false),
	}
	st := stateOf(t, earlier, fixtureTurn("Audit every module and report.", 24, 1, 1200))
	provider := &compactCannedProvider{t: t, summary: "the first modules were audited"}
	const window, overhead = 16000, 1500
	conv := conversationTokens(windowOf(st), false)
	ag, logs := windowAgent(t, st, comp, provider, window, overhead, conv)

	if !ag.maybeAutoCompact(context.Background()) {
		t.Fatalf("the turn is over the threshold and must be folded:\n%s", logs)
	}
	window_ := windowOf(st)
	row := window_[1]
	if !row.CompactionBlock || !strings.HasPrefix(row.Content, "Audit every module and report.") {
		t.Fatalf("the fold did not add its row after the earlier one: %q", firstChars(transcriptText(window_), 200))
	}
	target := inTurnTargetTokens(window, config.CompactionDefaultThresholdPercent)
	// What the aim does not pay for is the new row itself (its summary and the
	// prompt in front of it); everything else has to fit.
	if next, room := ag.inTurnRequestTokens(1), target+session.EstimateContextTokens(row.Content)+50; next > room {
		t.Fatalf("the fold left the next request at %d tokens, over its aim of %d plus its own row:\n%s", next, room, logs)
	}
}
