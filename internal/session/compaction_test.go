package session

import (
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
			msgs: []llm.Message{summary, assistantMsg("a")},
			keep: 0, wantOK: false,
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
