package session

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func userMsg(text string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: text}
}

func assistantMsg(text string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: text}
}

func exchangeMessages(n int) []llm.Message {
	var out []llm.Message
	for i := 0; i < n; i++ {
		out = append(out, userMsg("q"), assistantMsg("a"))
	}
	return out
}

func TestCompactionSplitIndex(t *testing.T) {
	summary := NewCompactionSummaryMessage("old summary", "m")

	cases := []struct {
		name    string
		msgs    []llm.Message
		keep    int
		wantIdx int
		wantOK  bool
	}{
		{name: "empty history", msgs: nil, keep: 2, wantOK: false},
		{name: "fewer user turns than keep", msgs: exchangeMessages(2), keep: 2, wantOK: false},
		{name: "exactly keep user turns", msgs: exchangeMessages(2), keep: 2, wantOK: false},
		{
			name: "keeps last two of three exchanges",
			msgs: exchangeMessages(3), keep: 2,
			// Index of the 2nd-from-last user message: u0,a0,u1,a1,u2,a2 -> 2.
			wantIdx: 2, wantOK: true,
		},
		{
			name: "keep zero compacts the whole window",
			msgs: exchangeMessages(2), keep: 0,
			wantIdx: 4, wantOK: true,
		},
		{name: "keep zero with empty history", msgs: nil, keep: 0, wantOK: false},
		{
			name: "counts only turns after the previous summary",
			// 2 exchanges, then a summary, then 2 more exchanges: with keep 2
			// there is nothing new to compact after the summary boundary... the
			// window has exactly 2 user turns, so no split.
			msgs: append(append(exchangeMessages(2), summary), exchangeMessages(2)...),
			keep: 2, wantOK: false,
		},
		{
			name: "chained compaction includes the previous summary in the head",
			// summary at index 4, then 3 exchanges: window user turns = 3 > keep 2,
			// boundary is the 2nd-from-last user message at absolute index 7.
			msgs: append(append(exchangeMessages(2), summary), exchangeMessages(3)...),
			keep: 2, wantIdx: 7, wantOK: true,
		},
		{
			name: "summary marker itself is not a user turn",
			msgs: []llm.Message{summary},
			keep: 0, wantOK: false,
		},
		{
			name: "a summary followed by a user message is still not a user turn",
			msgs: []llm.Message{summary, userMsg("q"), assistantMsg("a")},
			keep: 1, wantOK: false,
		},
		{
			name: "a summary followed by a user message keeps its old split",
			msgs: append([]llm.Message{summary}, exchangeMessages(3)...),
			keep: 2, wantIdx: 3, wantOK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx, ok := CompactionSplitIndex(tc.msgs, tc.keep)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && idx != tc.wantIdx {
				t.Fatalf("idx = %d, want %d", idx, tc.wantIdx)
			}
		})
	}
}

func TestMessagesForLLM(t *testing.T) {
	plain := exchangeMessages(2)
	if got := MessagesForLLM(plain); len(got) != len(plain) {
		t.Fatalf("without summary: got %d messages, want %d", len(got), len(plain))
	}

	s1 := NewCompactionSummaryMessage("first", "m")
	s2 := NewCompactionSummaryMessage("second", "m")
	msgs := append(append(exchangeMessages(2), s1), exchangeMessages(1)...)
	msgs = append(append(msgs, s2), exchangeMessages(1)...)

	got := MessagesForLLM(msgs)
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3 (summary + last exchange)", len(got))
	}
	if !got[0].CompactionSummary || !strings.Contains(got[0].Content, "second") {
		t.Fatalf("window must start from the last summary, got %+v", got[0])
	}
}

func blockMsg(text string) llm.Message {
	m := NewCompactionSummaryMessage(text, "m")
	m.CompactionBlock = true
	return m
}

// TestLLMWindowReadsTheChain holds which summary rows the model reads: the
// last row that starts a chain and every block after it, never a row before
// that start, and the tail after the last row.
func TestLLMWindowReadsTheChain(t *testing.T) {
	root := NewCompactionSummaryMessage("root", "m")
	cases := []struct {
		name       string
		msgs       []llm.Message
		wantBlocks []int
		wantTail   int
	}{
		{name: "no summary", msgs: exchangeMessages(2), wantTail: 0},
		{
			name: "one row written before blocks existed",
			// u a S u a
			msgs:       []llm.Message{userMsg("q"), assistantMsg("a"), root, userMsg("q"), assistantMsg("a")},
			wantBlocks: []int{2}, wantTail: 3,
		},
		{
			name: "several unmarked rows read only the last",
			// u S u S u
			msgs:       []llm.Message{userMsg("q"), root, userMsg("q"), root, userMsg("q")},
			wantBlocks: []int{3}, wantTail: 4,
		},
		{
			name: "a root and the blocks after it",
			// u S u B u B u
			msgs:       []llm.Message{userMsg("q"), root, userMsg("q"), blockMsg("b1"), userMsg("q"), blockMsg("b2"), userMsg("q")},
			wantBlocks: []int{1, 3, 5}, wantTail: 6,
		},
		{
			name: "blocks with no root reach back to the first",
			// u B u B u
			msgs:       []llm.Message{userMsg("q"), blockMsg("b1"), userMsg("q"), blockMsg("b2"), userMsg("q")},
			wantBlocks: []int{1, 3}, wantTail: 4,
		},
		{
			name: "a merge row cuts off the blocks it retold",
			// B u B M u B u: the merge after b2 retold b1 and b2.
			msgs:       []llm.Message{blockMsg("b1"), userMsg("q"), blockMsg("b2"), root, userMsg("q"), blockMsg("b3"), userMsg("q")},
			wantBlocks: []int{3, 5}, wantTail: 6,
		},
		{
			name:       "a block at the very end leaves an empty tail",
			msgs:       []llm.Message{userMsg("q"), blockMsg("b1")},
			wantBlocks: []int{1}, wantTail: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blocks, tail := LLMWindow(tc.msgs)
			if !slices.Equal(blocks, tc.wantBlocks) || tail != tc.wantTail {
				t.Fatalf("LLMWindow = %v, %d; want %v, %d", blocks, tail, tc.wantBlocks, tc.wantTail)
			}
		})
	}
}

func TestMessagesForLLMReadsTheBlocksInOrderThenTheTail(t *testing.T) {
	msgs := []llm.Message{
		userMsg("q1"), assistantMsg("a1"), blockMsg("b1"),
		userMsg("q2"), assistantMsg("a2"), blockMsg("b2"),
		userMsg("q3"), assistantMsg("a3"),
	}
	var got []string
	for _, m := range MessagesForLLM(msgs) {
		got = append(got, m.Content)
	}
	want := []string{msgs[2].Content, msgs[5].Content, "q3", "a3"}
	if !slices.Equal(got, want) {
		t.Fatalf("MessagesForLLM = %q, want %q", got, want)
	}
	// The window is a fresh slice: writing to it must not reach the transcript.
	MessagesForLLM(msgs)[0].Content = "changed"
	if msgs[2].Content == "changed" {
		t.Fatal("MessagesForLLM shares its backing array with the transcript for a chain of blocks")
	}
}

// TestCompactionSplitIndexNeverFoldsTheChain holds the boundary of a chained
// compaction: the turns are counted in the tail after the last block, so the
// head to summarize starts at the tail and no summary row is in it.
func TestCompactionSplitIndexNeverFoldsTheChain(t *testing.T) {
	// u a B u a B u a u a u a
	msgs := []llm.Message{
		userMsg("q1"), assistantMsg("a1"), blockMsg("b1"),
		userMsg("q2"), assistantMsg("a2"), blockMsg("b2"),
		userMsg("q3"), assistantMsg("a3"),
		userMsg("q4"), assistantMsg("a4"),
		userMsg("q5"), assistantMsg("a5"),
	}
	idx, ok := CompactionSplitIndex(msgs, 2)
	if !ok || idx != 8 {
		t.Fatalf("CompactionSplitIndex = %d, %v; want 8, true", idx, ok)
	}
	_, tail := LLMWindow(msgs)
	for _, m := range msgs[tail:idx] {
		if m.CompactionSummary {
			t.Fatalf("the head to fold carries a summary row: %+v", m)
		}
	}
	// Two turns in the tail and keep 2: nothing to fold, whatever the chain
	// before the tail holds.
	if _, ok := CompactionSplitIndex(msgs[:10], 2); ok {
		t.Fatal("a tail of exactly keep turns has nothing to fold")
	}
}

func TestNewCompactionSummaryMessage(t *testing.T) {
	m := NewCompactionSummaryMessage("  the summary body  ", "prov/model")
	if m.Role != llm.RoleUser {
		t.Fatalf("role = %q, want user", m.Role)
	}
	if !m.CompactionSummary {
		t.Fatal("marker flag not set")
	}
	if !strings.Contains(m.Content, "the summary body") {
		t.Fatalf("content lost the summary: %q", m.Content)
	}
	if m.Model != "prov/model" {
		t.Fatalf("model = %q", m.Model)
	}
	if m.CreatedAt == "" {
		t.Fatal("CreatedAt must be set")
	}
}

func TestInsertCompactionSummary(t *testing.T) {
	st := &State{ID: "s", Messages: exchangeMessages(3)}
	persisted := 0
	st.SetPersistHook(func() { persisted++ })

	sum := NewCompactionSummaryMessage("sum", "m")
	st.InsertCompactionSummary(2, sum)

	msgs := st.GetMessages()
	if len(msgs) != 7 {
		t.Fatalf("len = %d, want 7", len(msgs))
	}
	if !msgs[2].CompactionSummary {
		t.Fatalf("summary not at index 2: %+v", msgs[2])
	}
	if msgs[3].Content != "q" || msgs[3].Role != llm.RoleUser {
		t.Fatalf("tail shifted incorrectly: %+v", msgs[3])
	}
	if persisted == 0 {
		t.Fatal("insert must persist the session")
	}

	// Out-of-range index appends at the end instead of panicking.
	st.InsertCompactionSummary(100, NewCompactionSummaryMessage("tail", "m"))
	msgs = st.GetMessages()
	if !msgs[len(msgs)-1].CompactionSummary {
		t.Fatal("out-of-range insert must append")
	}
}

// TestSessionDigestCarriesEveryRowOfTheChain holds what "@session:" shows of a
// session compacted more than once: every summary row of the chain, oldest
// first, as its earlier conversation, and none of them as a message of its own.
func TestSessionDigestCarriesEveryRowOfTheChain(t *testing.T) {
	st := &State{ID: "sess_digest_chain", CWD: t.TempDir()}
	for _, m := range []llm.Message{
		userMsg("q1"), assistantMsg("a1"), blockMsg("FIRST-BLOCK"),
		userMsg("q2"), assistantMsg("a2"), blockMsg("SECOND-BLOCK"),
		userMsg("q3"), assistantMsg("a3"),
	} {
		st.AddMessage(m)
	}
	r := &mentionResolver{
		store: &FileStore{Root: t.TempDir()},
		live:  func(string) *State { return st },
	}
	digest, _, err := r.sessionDigest(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, earlier, ok := strings.Cut(digest, "Summary of its earlier conversation:\n")
	if !ok {
		t.Fatalf("the digest has no earlier conversation:\n%s", digest)
	}
	first, second := strings.Index(earlier, "FIRST-BLOCK"), strings.Index(earlier, "SECOND-BLOCK")
	if first < 0 || second < first {
		t.Fatalf("the earlier conversation does not carry both rows in order:\n%s", digest)
	}
	if n := strings.Count(digest, "SECOND-BLOCK"); n != 1 {
		t.Fatalf("the second row is in the digest %d times, want once:\n%s", n, digest)
	}
}

// TestSessionDigestOfALongChainKeepsTheNewestRows: a chain larger than the
// digest's share of the earlier conversation loses its oldest rows, not its
// newest ones, which carry what the session was doing last.
func TestSessionDigestOfALongChainKeepsTheNewestRows(t *testing.T) {
	st := &State{ID: "sess_digest_long_chain", CWD: t.TempDir()}
	for i := 1; i <= 5; i++ {
		st.AddMessage(userMsg(fmt.Sprintf("q%d", i)))
		st.AddMessage(assistantMsg(fmt.Sprintf("a%d", i)))
		st.AddMessage(blockMsg(fmt.Sprintf("BLOCK-%d ", i) + strings.Repeat("x", sessionDigestBytes/6)))
	}
	r := &mentionResolver{
		store: &FileStore{Root: t.TempDir()},
		live:  func(string) *State { return st },
	}
	digest, _, err := r.sessionDigest(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(digest, "BLOCK-5 ") || strings.Contains(digest, "BLOCK-1 ") {
		t.Fatalf("the digest does not keep the newest rows of the chain:\n%s", digest)
	}
}
