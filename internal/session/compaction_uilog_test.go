package session

// A summary row is a user-role row, and the UI log numbers its entries by the
// user-role rows before them: inserting a row has to renumber them, or a notice
// logged earlier in the turn renders in front of a summary written in its middle
// (issue #490).

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// uiLogPlacements returns, for each entry of the log by its message, the index
// of the message it renders right before in a read of the whole history
// (len(msgs) is the end), found the way a paged read finds it: through
// MessagePage.UILog, one message per page.
func uiLogPlacements(t *testing.T, msgs []llm.Message, log []UILogEntry) map[string]int {
	t.Helper()
	got := map[string]int{}
	n := len(msgs)
	for p := 0; p <= n; p++ {
		// The faked total keeps the entries of the end of the history out of every
		// page but the one past the last message.
		page := MessagePage{Offset: p, End: min(p+1, n), Total: n + 1}
		if p == n {
			page = MessagePage{Offset: n, End: n, Total: n}
		}
		for _, e := range page.UILog(msgs, log) {
			if prev, dup := got[e.Message]; dup {
				t.Fatalf("entry %q is served twice (before %d and %d)", e.Message, prev, p)
			}
			got[e.Message] = p
		}
	}
	for _, e := range log {
		if _, ok := got[e.Message]; !ok {
			t.Fatalf("entry %q is served by no page", e.Message)
		}
	}
	return got
}

// threeTurns is u0 a0 u1 a1 u2 a2 with a notice logged at the end of each turn,
// as the agent logs one: stamped with the number of user-role rows so far.
func threeTurns() *State {
	st := &State{ID: "s_uilog"}
	for i, text := range []string{"first", "second", "third"} {
		st.AddMessage(userMsg(text))
		st.AddMessage(assistantMsg("answer " + text))
		st.AppendUILogNotice(CountUserTurns(st.GetMessages()), "after turn "+string(rune('0'+i)))
	}
	return st
}

func TestInsertCompactionSummaryAtATurnBoundaryKeepsTheNoticesOfTheKeptTurns(t *testing.T) {
	st := threeTurns()
	// A regular fold: the first two turns are summarized, the row goes in front
	// of the third.
	st.InsertCompactionSummary(4, NewCompactionSummaryMessage("the first two turns", "m"))

	msgs := st.GetMessages()
	at := uiLogPlacements(t, msgs, st.GetUILog())
	// u0 a0 u1 a1 S u2 a2: the notice that ended turn 1 stays in front of the row,
	// the one that ended turn 2 stays at the end of turn 2.
	want := map[string]int{"after turn 0": 2, "after turn 1": 4, "after turn 2": len(msgs)}
	for msg, pos := range want {
		if at[msg] != pos {
			t.Errorf("%q renders before message %d, want %d", msg, at[msg], pos)
		}
	}
}

func TestInsertCompactionSummaryInsideATurnKeepsItsNoticesAtItsEnd(t *testing.T) {
	st := &State{ID: "s_uilog_mid"}
	st.AddMessage(userMsg("first"))
	st.AddMessage(assistantMsg("answer"))
	st.AppendUILogNotice(CountUserTurns(st.GetMessages()), "after turn 0")
	st.AddMessage(userMsg("second"))
	for _, m := range stepOf("c1") {
		st.AddMessage(m)
	}
	st.AppendUILogNotice(CountUserTurns(st.GetMessages()), "mid second turn")
	for _, m := range stepOf("c2") {
		st.AddMessage(m)
	}

	// An in-turn fold: the row goes in front of the second step.
	st.InsertCompactionSummary(5, NewInTurnCompactionSummaryMessage("second", "the first step", "m"))
	st.AddMessage(assistantMsg("done"))
	st.AddMessage(userMsg("third"))
	st.AddMessage(assistantMsg("answer"))
	st.AppendUILogNotice(CountUserTurns(st.GetMessages()), "after turn 2")

	msgs := st.GetMessages()
	at := uiLogPlacements(t, msgs, st.GetUILog())
	// u0 a0 u1 a1 t S a2 t done u2 a: the notice logged inside the second turn
	// stays at the end of that turn, in front of the third prompt, not in front of
	// the row in the middle of it.
	third := 0
	for i, m := range msgs {
		if m.Role == llm.RoleUser && m.Content == "third" {
			third = i
		}
	}
	want := map[string]int{"after turn 0": 2, "mid second turn": third, "after turn 2": len(msgs)}
	for msg, pos := range want {
		if at[msg] != pos {
			t.Errorf("%q renders before message %d, want %d", msg, at[msg], pos)
		}
	}
}

func TestInsertCompactionSummaryAtTheEndKeepsTheLastNoticeInFrontOfIt(t *testing.T) {
	st := threeTurns()
	// A manual compaction that keeps nothing appends the row.
	st.InsertCompactionSummary(100, NewCompactionSummaryMessage("everything", "m"))

	msgs := st.GetMessages()
	at := uiLogPlacements(t, msgs, st.GetUILog())
	if want := len(msgs) - 1; at["after turn 2"] != want {
		t.Errorf("the last notice renders before message %d, want %d (the row, which ends the folded turn)", at["after turn 2"], want)
	}
	if at["after turn 0"] != 2 || at["after turn 1"] != 4 {
		t.Errorf("earlier notices moved: %v", at)
	}
}

func TestInsertCompactionSummaryWithoutALogTouchesNothing(t *testing.T) {
	st := &State{ID: "s_nolog", Messages: exchangeMessages(3)}
	st.InsertCompactionSummary(2, NewCompactionSummaryMessage("sum", "m"))
	if log := st.GetUILog(); len(log) != 0 {
		t.Fatalf("log = %+v, want none", log)
	}
}

// A rewind compares the stamps with the user-role rows of the surviving prefix:
// the notices of the turn that is cut go with it, those of the turns before stay.
func TestRewindAfterASummaryKeepsTheNoticesOfTheTurnsBeforeIt(t *testing.T) {
	st := threeTurns()
	st.InsertCompactionSummary(4, NewCompactionSummaryMessage("the first two turns", "m"))

	// Real user messages: first, second, third. Cutting the third drops its
	// notice and keeps the other two.
	if _, _, err := st.TruncateMessagesBeforeUserN(2, nil); err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, e := range st.GetUILog() {
		kept = append(kept, e.Message)
	}
	if len(kept) != 2 || kept[0] != "after turn 0" || kept[1] != "after turn 1" {
		t.Fatalf("notices kept = %q, want those of the first two turns", kept)
	}
}
