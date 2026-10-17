package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// chainBlock is a block of the chain whose summary is about tokens estimate
// tokens long and names itself.
func chainBlock(name string, tokens int) llm.Message {
	return session.NewCompactionBlockMessage(name+" "+strings.Repeat("x", tokens*3), "m", true)
}

func TestChainMergeCount(t *testing.T) {
	// A window of 30000 tokens: the chain may take 9900, a merge aims at 4950.
	const window = 30000
	b := func(name string) llm.Message { return chainBlock(name, 2000) }
	turn := func(n int) []llm.Message {
		return []llm.Message{userText(fmt.Sprintf("q%d", n)), {Role: llm.RoleAssistant, Content: "a"}}
	}
	cases := []struct {
		name  string
		msgs  []llm.Message
		wantK int
		ok    bool
	}{
		{
			name: "a chain within its share is left alone",
			msgs: concatMsgs(turn(1), []llm.Message{b("b1")}, turn(2), []llm.Message{b("b2")}, turn(3)),
		},
		{
			name: "an outgrown chain merges its earliest rows down to the aim",
			// Six rows of ~2000: 12000 > 9900; merging five leaves ~2000 and a
			// merged row counted as long as the longest it retells, 4000 <= 4950.
			msgs: concatMsgs(
				turn(1), []llm.Message{b("b1")}, turn(2), []llm.Message{b("b2")}, turn(3), []llm.Message{b("b3")},
				turn(4), []llm.Message{b("b4")}, turn(5), []llm.Message{b("b5")}, turn(6), []llm.Message{b("b6")}, turn(7)),
			wantK: 5, ok: true,
		},
		{
			name: "the rows of the turn in progress go last, when the earlier ones are not enough",
			// The turn's prompt was folded by an in-turn fold: q4 is before b4, so
			// b4..b6 are the turn's. Merging b1..b3 would leave 6000 and the merged
			// row; b4 and b5 go as well, the newest row stays.
			msgs: concatMsgs(
				turn(1), []llm.Message{b("b1")}, turn(2), []llm.Message{b("b2")}, turn(3), []llm.Message{b("b3")},
				[]llm.Message{userText("q4"), {Role: llm.RoleAssistant, Content: "step"}}, []llm.Message{b("b4")},
				[]llm.Message{{Role: llm.RoleAssistant, Content: "step"}}, []llm.Message{b("b5")},
				[]llm.Message{{Role: llm.RoleAssistant, Content: "step"}}, []llm.Message{b("b6")},
				[]llm.Message{{Role: llm.RoleAssistant, Content: "step"}}),
			wantK: 5, ok: true,
		},
		{
			name: "one row that may go is no merge",
			msgs: concatMsgs(
				turn(1), []llm.Message{chainBlock("b1", 6000)},
				[]llm.Message{userText("q2"), {Role: llm.RoleAssistant, Content: "step"}}, []llm.Message{chainBlock("b2", 6000)},
				[]llm.Message{{Role: llm.RoleAssistant, Content: "step"}}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, ok := chainMergeCount(tc.msgs, window)
			if k != tc.wantK || ok != tc.ok {
				t.Fatalf("chainMergeCount = %d, %v; want %d, %v", k, ok, tc.wantK, tc.ok)
			}
		})
	}
}

// A compaction that leaves the chain over its share merges the earliest rows
// into one row that starts the chain: inserted after the last row it retells,
// with the rows after it and the new block untouched, and the transcript
// keeping every row it replaced.
func TestCompactSessionMergesAnOutgrownChain(t *testing.T) {
	var parts [][]llm.Message
	for i := 1; i <= 5; i++ {
		parts = append(parts,
			[]llm.Message{userText(fmt.Sprintf("question %d", i)), {Role: llm.RoleAssistant, Content: fmt.Sprintf("answer %d", i)}},
			[]llm.Message{chainBlock(fmt.Sprintf("B%d", i), 2000)})
	}
	parts = append(parts,
		[]llm.Message{userText("question 6"), {Role: llm.RoleAssistant, Content: "answer 6"}},
		[]llm.Message{userText("question 7"), {Role: llm.RoleAssistant, Content: "answer 7"}})
	st := stateOf(t, parts...)
	keep := 1
	provider := &compactCannedProvider{t: t, summary: "SUMMARY"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)
	ag.cfg.Models[0].MaxContextTokens = 30000

	res, err := ag.CompactSession(context.Background(), CompactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Five rows of ~2000 and the new one of a few tokens against a share of
	// 9900: merging four leaves ~2000, and with a merged row as long as one of
	// them ~4000, under the aim of 4950.
	if res.MergedSummaries != 4 {
		t.Fatalf("merged %d rows, want 4", res.MergedSummaries)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("the summarizer was called %d times, want the fold and the merge", len(provider.requests))
	}
	merge := provider.requests[1][1].Content
	for i := 1; i <= 5; i++ {
		if in, want := strings.Contains(merge, fmt.Sprintf("B%d ", i)), i <= 4; in != want {
			t.Fatalf("row B%d in the merge request = %v, want %v", i, in, want)
		}
	}

	window := windowOf(st)
	if !window[0].CompactionSummary || window[0].CompactionBlock {
		t.Fatalf("the window does not open with the merged row: %+v", window[0])
	}
	if !strings.Contains(window[1].Content, "B5 ") {
		t.Fatalf("the row after the merge is not B5: %q", firstChars(window[1].Content, 120))
	}
	if !window[2].CompactionBlock || !strings.HasSuffix(window[2].Content, "SUMMARY") {
		t.Fatalf("the new block is not after B5: %+v", window[2])
	}
	if got := summaryRowsOf(st); got != 7 {
		t.Fatalf("the transcript holds %d summary rows, want the 5 before, the new block and the merge", got)
	}
	// The merged row sits right after B4 in the transcript.
	msgs := st.GetMessages()
	for i, m := range msgs {
		if m.CompactionSummary && !m.CompactionBlock {
			if !strings.Contains(msgs[i-1].Content, "B4 ") {
				t.Fatalf("the merged row follows %q, want B4", firstChars(msgs[i-1].Content, 60))
			}
		}
	}

	// The merge left room: the next compaction adds a block and merges nothing,
	// so the rows before it stay the bytes the provider cached.
	before := windowOf(st)[:3]
	st.AddMessage(userText("question 8"))
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "answer 8"})
	res, err = ag.CompactSession(context.Background(), CompactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.MergedSummaries != 0 {
		t.Fatalf("the compaction after a merge merged %d rows again", res.MergedSummaries)
	}
	after := windowOf(st)
	for i := range before {
		if after[i].Content != before[i].Content {
			t.Fatalf("row %d changed after a compaction with no merge", i)
		}
	}
}

// A merge the summarizer fails leaves the compaction done: the new block is in,
// the chain is as it was, and the failure is the merge's alone.
func TestCompactSessionKeepsTheBlockWhenTheMergeFails(t *testing.T) {
	var parts [][]llm.Message
	for i := 1; i <= 5; i++ {
		parts = append(parts,
			[]llm.Message{userText(fmt.Sprintf("question %d", i)), {Role: llm.RoleAssistant, Content: "a"}},
			[]llm.Message{chainBlock(fmt.Sprintf("B%d", i), 2000)})
	}
	parts = append(parts,
		[]llm.Message{userText("question 6"), {Role: llm.RoleAssistant, Content: "a"}},
		[]llm.Message{userText("question 7"), {Role: llm.RoleAssistant, Content: "a"}})
	st := stateOf(t, parts...)
	keep := 1
	provider := &failSecondProvider{compactCannedProvider: compactCannedProvider{t: t, summary: "SUMMARY"}}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)
	ag.cfg.Models[0].MaxContextTokens = 30000

	res, err := ag.CompactSession(context.Background(), CompactOptions{})
	if err != nil {
		t.Fatalf("the compaction failed with its merge: %v", err)
	}
	if res.MergedSummaries != 0 || res.MergeError == "" {
		t.Fatalf("result = %+v, want no rows merged and the merge's failure reported", res)
	}
	if got := summaryRowsOf(st); got != 6 {
		t.Fatalf("the transcript holds %d summary rows, want the 5 before and the new block", got)
	}
}

// failSecondProvider answers the first summarization call and refuses the rest.
type failSecondProvider struct {
	compactCannedProvider
}

func (p *failSecondProvider) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	if len(p.requests) >= 1 {
		p.requests = append(p.requests, messages)
		return nil, fmt.Errorf("503 Service Unavailable")
	}
	return p.compactCannedProvider.Complete(ctx, messages, tools)
}

// chainTokens is what the summary rows of the window take, in estimate tokens.
func chainTokens(st *session.State) int {
	n := 0
	for _, m := range windowOf(st) {
		if m.CompactionSummary {
			n += session.EstimateContextTokens(m.Content)
		}
	}
	return n
}

// One long turn folded again and again keeps its chain within its share of the
// window. Its own folds are all it has to merge, so they are merged too, into a
// row that keeps the turn's prompt in front: the model still reads the prompt
// exactly once, and the turn can go on folding on a small window (#490).
func TestALongTurnFoldedManyTimesKeepsItsChainWithinItsShare(t *testing.T) {
	const prompt = "UNIQUE-REQUEST-TEXT: audit every module and report."
	const window = 30000
	st := inTurnSession(t, prompt, 6)
	provider := &compactCannedProvider{t: t, summary: "WORK " + strings.Repeat("x", 1500*3)}
	ag := compactTestAgent(t, st, config.Compaction{}, provider)
	ag.cfg.Models[0].MaxContextTokens = window
	share := window * compactionChainSharePercent / 100

	merged := 0
	for fold := 1; fold <= 20; fold++ {
		if fold > 1 {
			for _, m := range steps(fmt.Sprintf("f%d_", fold), 6) {
				st.AddMessage(m)
			}
		}
		res, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true})
		if err != nil {
			t.Fatalf("fold %d: %v", fold, err)
		}
		merged += res.MergedSummaries
		if got := chainTokens(st); got > share {
			t.Fatalf("fold %d: the chain takes %d tokens, over its share of %d", fold, got, share)
		}
		window := windowOf(st)
		if got := strings.Count(transcriptText(window), prompt); got != 1 {
			t.Fatalf("fold %d: the window holds the prompt %d times, want once", fold, got)
		}
		if !strings.HasPrefix(window[0].Content, prompt+"\n\n") {
			t.Fatalf("fold %d: the window does not open with the prompt: %q", fold, firstChars(window[0].Content, 120))
		}
		if issues := session.ValidateToolPairing(window); len(issues) > 0 {
			t.Fatalf("fold %d: malformed tool-call history: %v", fold, issues)
		}
	}
	if merged == 0 {
		t.Fatal("twenty folds of one turn never merged its rows")
	}
}

// A summarizer with a smaller window than the session's (compaction.model)
// reads the earlier summaries within its own budget: the oldest of them are
// left out of its request before any pass is refused, and the compaction goes
// through the configured summarizer.
func TestEarlierSummariesFitASmallerSummarizer(t *testing.T) {
	var parts [][]llm.Message
	for i := 1; i <= 3; i++ {
		parts = append(parts,
			[]llm.Message{userText(fmt.Sprintf("question %d", i)), {Role: llm.RoleAssistant, Content: "answer"}},
			[]llm.Message{chainBlock(fmt.Sprintf("B%d", i), 3000)})
	}
	parts = append(parts, fixtureTurn("question 4", 40, 1, 1500), []llm.Message{userText("question 5"), {Role: llm.RoleAssistant, Content: "answer"}})
	st := stateOf(t, parts...)
	keep := 1
	const small = 8000
	provider := &compactShrinkingProvider{t: t, limit: small * 3, summary: "SUMMARY"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep, Model: "fake/small"}, provider)
	ag.cfg.Models[0].MaxContextTokens = 128000
	ag.cfg.Models = append(ag.cfg.Models, config.ModelEntry{Model: "fake/small", MaxTokens: 100, MaxContextTokens: small})

	res, err := ag.CompactSession(context.Background(), CompactOptions{})
	if err != nil {
		t.Fatalf("the compaction failed: %v", err)
	}
	if provider.refusals > 0 || res.Model != "fake/small" {
		t.Fatalf("%d request(s) did not fit the summarizer's window; summarized by %s", provider.refusals, res.Model)
	}
	if !strings.Contains(transcriptText(provider.requests[0]), "B3 ") {
		t.Fatal("the newest earlier summary is not in the request")
	}
}

// A session that moved to a model with a smaller window carries a chain written
// for the larger one, larger than the new window by itself. The compaction still
// goes through, and the merge after it brings the chain back within its share
// of the window the session works with now.
func TestAChainLargerThanANewWindowIsMergedAfterTheCompaction(t *testing.T) {
	var parts [][]llm.Message
	for i := 1; i <= 12; i++ {
		parts = append(parts,
			[]llm.Message{userText(fmt.Sprintf("question %d", i)), {Role: llm.RoleAssistant, Content: "answer"}},
			[]llm.Message{chainBlock(fmt.Sprintf("B%d", i), 3000)})
	}
	parts = append(parts,
		[]llm.Message{userText("question 13"), {Role: llm.RoleAssistant, Content: "answer"}},
		[]llm.Message{userText("question 14"), {Role: llm.RoleAssistant, Content: "answer"}})
	st := stateOf(t, parts...)
	keep := 1
	const window = 32000
	provider := &compactShrinkingProvider{t: t, limit: window * 3, summary: "SUMMARY"}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)
	ag.cfg.Models[0].MaxContextTokens = window

	res, err := ag.CompactSession(context.Background(), CompactOptions{})
	if err != nil {
		t.Fatalf("the compaction failed: %v", err)
	}
	if provider.refusals > 0 {
		t.Fatalf("%d request(s) did not fit the summarizer's window", provider.refusals)
	}
	if res.MergedSummaries == 0 || res.MergeError != "" {
		t.Fatalf("result = %+v, want the chain merged", res)
	}
	if got, share := chainTokens(st), window*compactionChainSharePercent/100; got > share {
		t.Fatalf("the chain takes %d tokens after the merge, over its share of %d", got, share)
	}
}

// The merged row is counted when a merge decides how many rows to retell, so a
// summarizer that writes it as long as the rows it was given still brings the
// chain down to the aim, half of its share: the room the next blocks take
// before the next merge, during which the prefix the provider cached holds.
// The merge request asks for no more than that length.
func TestAMergeLeavesRoomForTheNextBlocks(t *testing.T) {
	st := seededCompactState(t, 2)
	keep := 1
	provider := &compactCannedProvider{t: t, summary: "S " + strings.Repeat("x", 2000*3)}
	ag := compactTestAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)
	const window = 30000
	ag.cfg.Models[0].MaxContextTokens = window
	aim := window * compactionChainSharePercent / 100 * compactionChainAimPercent / 100

	var merges []int
	for i := 1; i <= 14; i++ {
		res, err := ag.CompactSession(context.Background(), CompactOptions{})
		if err != nil {
			t.Fatalf("compaction %d: %v", i, err)
		}
		if res.MergedSummaries > 0 {
			if got := chainTokens(st); got > aim {
				t.Fatalf("compaction %d merged the chain down to %d tokens, over the aim of %d", i, got, aim)
			}
			merges = append(merges, i)
			if req := transcriptText(provider.requests[len(provider.requests)-1]); !strings.Contains(req, "no longer than the longest") {
				t.Fatalf("the merge request does not bound the merged row:\n%s", firstChars(req, 400))
			}
		}
		st.AddMessage(userText(fmt.Sprintf("question %d", i+2)))
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "answer"})
	}
	if len(merges) < 2 {
		t.Fatalf("merges at compactions %v, want the chain merged more than once over 14 compactions", merges)
	}
}
