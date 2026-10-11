package session

// The session remembers where the turn in progress opened (issue #490): the
// index of the message the agent appended when the turn started, kept in step
// with the rows a compaction inserts in front of it. A text-and-time record
// could not tell the prompt from a follow-up queued within the same second with
// the same words.

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// openedState is a session whose turn opened at the message with index open:
// earlier, a, P, b, F, c with P and F the same words written in the same second,
// the shape that text and time could not tell apart.
func openedState() (*State, int) {
	st := &State{ID: "s_opening"}
	for _, m := range []llm.Message{userMsg("older"), assistantMsg("a")} {
		st.AddMessage(m)
	}
	st.AddTurnOpening(at(userMsg("continue"), openedAt))
	for _, m := range concatMessages([]llm.Message{assistantMsg("b")}, []llm.Message{at(userMsg("continue"), openedAt)}, []llm.Message{assistantMsg("c")}) {
		st.AddMessage(m)
	}
	return st, 2
}

func openingOf(t *testing.T, st *State) int {
	t.Helper()
	idx, ok := OpeningPromptIndex(st.GetMessages(), st.TurnAnchor())
	if !ok {
		t.Fatal("no opening prompt found")
	}
	return idx
}

func TestStateRecordsWhereTheTurnOpened(t *testing.T) {
	t.Run("a follow-up with the same words and time is not the prompt", func(t *testing.T) {
		st, open := openedState()
		if got := openingOf(t, st); got != open {
			t.Fatalf("opening = %d, want %d: the probe [older, a, P, b, F, c] returned the follow-up", got, open)
		}
	})

	t.Run("a state that never recorded one falls back", func(t *testing.T) {
		st := &State{ID: "s_none"}
		for _, m := range []llm.Message{userMsg("older"), assistantMsg("a"), userMsg("the prompt"), assistantMsg("b")} {
			st.AddMessage(m)
		}
		if got := openingOf(t, st); got != 2 {
			t.Fatalf("opening = %d, want the last user message (2)", got)
		}
	})

	t.Run("a new turn replaces the record", func(t *testing.T) {
		st, _ := openedState()
		st.AddTurnOpening(userMsg("the next turn"))
		if got := openingOf(t, st); got != len(st.GetMessages())-1 {
			t.Fatalf("opening = %d, want the newest message (%d)", got, len(st.GetMessages())-1)
		}
	})

	t.Run("an ordinary message does not move it", func(t *testing.T) {
		st, open := openedState()
		st.AddMessage(userMsg("a follow-up"))
		if got := openingOf(t, st); got != open {
			t.Fatalf("opening = %d, want %d", got, open)
		}
	})
}

func TestInsertCompactionSummaryKeepsTheOpeningInStep(t *testing.T) {
	t.Run("a regular summary inserted in front of the prompt moves it down", func(t *testing.T) {
		st, open := openedState()
		st.InsertCompactionSummary(open-1, NewCompactionSummaryMessage("everything before", "m"))
		if got := openingOf(t, st); got != open+1 {
			t.Fatalf("opening = %d, want %d: the prompt moved down by the inserted row", got, open+1)
		}
	})

	t.Run("a summary inserted at the prompt's own index moves it down too", func(t *testing.T) {
		// The kept tail begins at the prompt, so the row lands where it stood.
		st, open := openedState()
		st.InsertCompactionSummary(open, NewCompactionSummaryMessage("everything before", "m"))
		if got := openingOf(t, st); got != open+1 {
			t.Fatalf("opening = %d, want %d", got, open+1)
		}
		if m := st.GetMessages()[open+1]; m.Content != "continue" || m.CompactionSummary {
			t.Fatalf("the record names %+v, want the prompt", m)
		}
	})

	t.Run("an in-turn row inserted after the prompt leaves it where it is", func(t *testing.T) {
		st, open := openedState()
		st.InsertCompactionSummary(4, NewInTurnCompactionSummaryMessage(TurnSourceUser, "continue", "the first step", "m"))
		if got := openingOf(t, st); got != open {
			t.Fatalf("opening = %d, want %d", got, open)
		}
	})

	t.Run("a row appended at the end leaves it where it is", func(t *testing.T) {
		st, open := openedState()
		st.InsertCompactionSummary(100, NewCompactionSummaryMessage("everything", "m"))
		if got := openingOf(t, st); got != open {
			t.Fatalf("opening = %d, want %d", got, open)
		}
	})

	t.Run("a row inserted without a record records nothing", func(t *testing.T) {
		st := &State{ID: "s_none"}
		for _, m := range []llm.Message{userMsg("older"), assistantMsg("a"), userMsg("the prompt")} {
			st.AddMessage(m)
		}
		st.InsertCompactionSummary(1, NewCompactionSummaryMessage("older", "m"))
		if st.TurnAnchor() != (TurnAnchor{}) {
			t.Fatalf("a record appeared: %+v", st.TurnAnchor())
		}
	})
}

// A history that is rewound or replaced is not the one the record described.
func TestRewindForgetsTheOpening(t *testing.T) {
	st, _ := openedState()
	if _, _, err := st.TruncateMessagesBeforeUserN(1, nil); err != nil {
		t.Fatal(err)
	}
	if st.TurnAnchor() != (TurnAnchor{}) {
		t.Fatalf("the record survived a rewind: %+v", st.TurnAnchor())
	}

	// Undoing a rewind puts a tail back, which is not the turn the record named.
	st, _ = openedState()
	if err := st.restoreRewoundTail(2, []llm.Message{userMsg("restored"), assistantMsg("a")}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if st.TurnAnchor() != (TurnAnchor{}) {
		t.Fatalf("the record survived an undone rewind: %+v", st.TurnAnchor())
	}

	st, _ = openedState()
	st.ReplaceMessagesWithoutPersist([]llm.Message{userMsg("loaded"), assistantMsg("a")})
	if st.TurnAnchor() != (TurnAnchor{}) {
		t.Fatalf("the record survived a replaced history: %+v", st.TurnAnchor())
	}
}
