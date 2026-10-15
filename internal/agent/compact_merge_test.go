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
			// Six rows of ~2000: 12000 > 9900; merging four leaves ~4000 <= 4950.
			msgs: concatMsgs(
				turn(1), []llm.Message{b("b1")}, turn(2), []llm.Message{b("b2")}, turn(3), []llm.Message{b("b3")},
				turn(4), []llm.Message{b("b4")}, turn(5), []llm.Message{b("b5")}, turn(6), []llm.Message{b("b6")}, turn(7)),
			wantK: 4, ok: true,
		},
		{
			name: "the rows of the turn in progress are never merged",
			// The turn's prompt was folded by an in-turn fold: q4 is before b4, so
			// b4..b6 are the turn's and only b1..b3 may go.
			msgs: concatMsgs(
				turn(1), []llm.Message{b("b1")}, turn(2), []llm.Message{b("b2")}, turn(3), []llm.Message{b("b3")},
				[]llm.Message{userText("q4"), {Role: llm.RoleAssistant, Content: "step"}}, []llm.Message{b("b4")},
				[]llm.Message{{Role: llm.RoleAssistant, Content: "step"}}, []llm.Message{b("b5")},
				[]llm.Message{{Role: llm.RoleAssistant, Content: "step"}}, []llm.Message{b("b6")},
				[]llm.Message{{Role: llm.RoleAssistant, Content: "step"}}),
			wantK: 3, ok: true,
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
	// 9900: merging three leaves ~4100, under the aim of 4950.
	if res.MergedSummaries != 3 {
		t.Fatalf("merged %d rows, want 3", res.MergedSummaries)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("the summarizer was called %d times, want the fold and the merge", len(provider.requests))
	}
	merge := provider.requests[1][1].Content
	for i := 1; i <= 5; i++ {
		if in, want := strings.Contains(merge, fmt.Sprintf("B%d ", i)), i <= 3; in != want {
			t.Fatalf("row B%d in the merge request = %v, want %v", i, in, want)
		}
	}

	window := windowOf(st)
	if !window[0].CompactionSummary || window[0].CompactionBlock {
		t.Fatalf("the window does not open with the merged row: %+v", window[0])
	}
	if !strings.Contains(window[1].Content, "B4 ") || !strings.Contains(window[2].Content, "B5 ") {
		t.Fatalf("the rows after the merge are not B4 and B5: %q", firstChars(transcriptText(window[1:3]), 120))
	}
	if !window[3].CompactionBlock || !strings.HasSuffix(window[3].Content, "SUMMARY") {
		t.Fatalf("the new block is not after B5: %+v", window[3])
	}
	if got := summaryRowsOf(st); got != 7 {
		t.Fatalf("the transcript holds %d summary rows, want the 5 before, the new block and the merge", got)
	}
	// The merged row sits right after B3 in the transcript.
	msgs := st.GetMessages()
	for i, m := range msgs {
		if m.CompactionSummary && !m.CompactionBlock {
			if !strings.Contains(msgs[i-1].Content, "B3 ") {
				t.Fatalf("the merged row follows %q, want B3", firstChars(msgs[i-1].Content, 60))
			}
		}
	}

	// The merge left room: the next compaction adds a block and merges nothing,
	// so the rows before it stay the bytes the provider cached.
	before := windowOf(st)[:4]
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
