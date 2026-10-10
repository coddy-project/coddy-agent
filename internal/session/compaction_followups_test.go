package session

// The opening prompt of a turn and the follow-ups that come after it (issue
// #490): which user message a turn in progress started from, which messages
// after it are the user's own words, and the boundaries a compaction may cut at.

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// at returns m written at the given time, the way the agent stamps a message.
func at(m llm.Message, createdAt string) llm.Message {
	m.CreatedAt = createdAt
	return m
}

const (
	openedAt   = "2026-10-11T10:00:00Z"
	queuedAt   = "2026-10-11T10:03:00Z"
	stopHookAt = "2026-10-11T10:05:00Z"
)

// followUpTurn is a turn that opened with a prompt and went on through a queued
// follow-up and a Stop-hook follow-up, with earlier history in front of it:
//
//	0 earlier(u) 1 done(a) 2 opening(u) 3-4 step c1 5 queued(u) 6-7 step c2 8 stop(u) 9-10 step c3
func followUpTurn() []llm.Message {
	return concatMessages(
		[]llm.Message{userMsg("earlier"), assistantMsg("done"), at(userMsg("fix the build"), openedAt)},
		stepOf("c1"),
		[]llm.Message{at(userMsg("also keep the tests green"), queuedAt)},
		stepOf("c2"),
		[]llm.Message{at(userMsg(StopHookPrefix+"run the linter"), stopHookAt)},
		stepOf("c3"),
	)
}

func TestOpeningPromptIndex(t *testing.T) {
	turn := followUpTurn()
	opening := AnchorOf(turn[2])

	t.Run("the recorded prompt is found after queued and Stop-hook follow-ups", func(t *testing.T) {
		if idx, ok := OpeningPromptIndex(turn, opening); !ok || idx != 2 {
			t.Fatalf("idx = %d ok = %v, want 2", idx, ok)
		}
	})

	t.Run("the record survives a compaction inserting rows before the prompt", func(t *testing.T) {
		msgs := concatMessages(turn[:2], []llm.Message{NewCompactionSummaryMessage("everything before", "m")}, turn[2:])
		if idx, ok := OpeningPromptIndex(msgs, opening); !ok || idx != 3 {
			t.Fatalf("idx = %d ok = %v, want 3: the prompt moved down by the inserted row", idx, ok)
		}
	})

	t.Run("without a record it is the last user message that is not a Stop-hook follow-up", func(t *testing.T) {
		// The queued follow-up cannot be told from the prompt: this is the
		// fallback's known limit, and the reason the agent keeps a record.
		if idx, ok := OpeningPromptIndex(turn, TurnAnchor{}); !ok || idx != 5 {
			t.Fatalf("idx = %d ok = %v, want 5 (the queued follow-up)", idx, ok)
		}
		plain := concatMessages([]llm.Message{userMsg("old"), assistantMsg("a"), userMsg("the prompt")}, stepOf("c1"),
			[]llm.Message{userMsg(StopHookPrefix + "again")}, stepOf("c2"))
		if idx, ok := OpeningPromptIndex(plain, TurnAnchor{}); !ok || idx != 2 {
			t.Fatalf("idx = %d ok = %v, want 2: a Stop-hook follow-up is never the prompt", idx, ok)
		}
	})

	t.Run("a record that matches nothing falls back", func(t *testing.T) {
		ghost := AnchorOf(at(userMsg("not in this transcript"), openedAt))
		if idx, ok := OpeningPromptIndex(turn, ghost); !ok || idx != 5 {
			t.Fatalf("idx = %d ok = %v, want the fallback 5", idx, ok)
		}
	})

	t.Run("the same words at another time are another turn", func(t *testing.T) {
		msgs := concatMessages(
			[]llm.Message{at(userMsg("continue"), "2026-10-11T09:00:00Z"), assistantMsg("a")},
			[]llm.Message{at(userMsg("continue"), "2026-10-11T09:30:00Z"), assistantMsg("b")},
			[]llm.Message{at(userMsg("continue"), openedAt)}, stepOf("c1"),
			[]llm.Message{at(userMsg("continue"), queuedAt)}, stepOf("c2"),
		)
		if idx, ok := OpeningPromptIndex(msgs, AnchorOf(msgs[4])); !ok || idx != 4 {
			t.Fatalf("idx = %d ok = %v, want 4", idx, ok)
		}
	})

	t.Run("a woken turn's first message is its prompt", func(t *testing.T) {
		wake := at(userMsg("background task finished"), openedAt)
		wake.BackgroundWake = &llm.BackgroundWake{}
		msgs := concatMessages([]llm.Message{userMsg("old"), assistantMsg("a"), wake}, stepOf("c1"))
		if idx, ok := OpeningPromptIndex(msgs, AnchorOf(wake)); !ok || idx != 2 {
			t.Fatalf("idx = %d ok = %v, want 2", idx, ok)
		}
	})

	t.Run("a summary row is never the prompt", func(t *testing.T) {
		msgs := []llm.Message{NewCompactionSummaryMessage("only a summary", "m"), assistantMsg("a")}
		if idx, ok := OpeningPromptIndex(msgs, TurnAnchor{}); ok {
			t.Fatalf("idx = %d for a history with no user message", idx)
		}
	})
}

func TestTurnPromptIgnoresFollowUps(t *testing.T) {
	turn := followUpTurn()
	got, ok := TurnPrompt(turn, AnchorOf(turn[2]))
	if !ok || got != "fix the build" {
		t.Fatalf("got %q ok = %v, want the opening prompt, not a follow-up", got, ok)
	}
	// The Stop-hook follow-up is never taken for the prompt, with a record or
	// without.
	if got, ok := TurnPrompt(turn[:6], TurnAnchor{}); !ok || got != "also keep the tests green" {
		t.Fatalf("fallback got %q ok = %v", got, ok)
	}
	onlyHook := concatMessages([]llm.Message{userMsg("the prompt")}, stepOf("c1"), []llm.Message{userMsg(StopHookPrefix + "again")})
	if got, ok := TurnPrompt(onlyHook, TurnAnchor{}); !ok || got != "the prompt" {
		t.Fatalf("got %q ok = %v: a Stop-hook follow-up stood in for the prompt", got, ok)
	}
}

func TestTurnFollowUps(t *testing.T) {
	turn := followUpTurn()

	t.Run("queued follow-ups verbatim, Stop-hook follow-ups left out", func(t *testing.T) {
		got := TurnFollowUps(turn, 2, len(turn))
		if len(got) != 1 || got[0] != "also keep the tests green" {
			t.Fatalf("follow-ups = %q, want only the queued one", got)
		}
	})

	t.Run("only those before the end of the folded head", func(t *testing.T) {
		if got := TurnFollowUps(turn, 2, 5); len(got) != 0 {
			t.Fatalf("follow-ups = %q before index 5, want none: the queued one is the first message of the tail", got)
		}
		if got := TurnFollowUps(turn, 2, 6); len(got) != 1 {
			t.Fatalf("follow-ups = %q before index 6, want the queued one", got)
		}
	})

	t.Run("follow-ups a fold hid behind a summary row are still found", func(t *testing.T) {
		msgs := concatMessages(turn[:8], []llm.Message{NewInTurnCompactionSummaryMessage("fix the build", "s", "m")}, turn[8:])
		got := TurnFollowUps(msgs, 2, len(msgs))
		if len(got) != 1 || got[0] != "also keep the tests green" {
			t.Fatalf("follow-ups = %q, want the queued one from before the row", got)
		}
	})

	t.Run("summary rows, blank messages and wake rows are no follow-up", func(t *testing.T) {
		wake := userMsg("background task finished")
		wake.BackgroundWake = &llm.BackgroundWake{}
		msgs := concatMessages(
			[]llm.Message{userMsg("prompt")}, stepOf("c1"),
			[]llm.Message{NewCompactionSummaryMessage("s", "m"), userMsg("  \n"), wake, userMsg("a real one")},
		)
		got := TurnFollowUps(msgs, 0, len(msgs))
		if len(got) != 1 || got[0] != "a real one" {
			t.Fatalf("follow-ups = %q, want only the real one", got)
		}
	})

	t.Run("an end past the history is the history", func(t *testing.T) {
		if got := TurnFollowUps(turn, 2, 1<<20); len(got) != 1 {
			t.Fatalf("follow-ups = %q", got)
		}
	})
}

// Only the user messages up to the opening prompt are turn boundaries for the
// split a turn in progress is compacted at: a follow-up inside the turn is not.
func TestCompactionSplitIndexUpToIgnoresFollowUps(t *testing.T) {
	turn := followUpTurn()
	const open = 2

	t.Run("the follow-up is not a boundary", func(t *testing.T) {
		idx, ok := CompactionSplitIndexUpTo(turn, 1, open)
		if !ok || idx != open {
			t.Fatalf("idx = %d ok = %v, want the opening prompt (%d)", idx, ok, open)
		}
	})

	t.Run("unbounded it cuts at the latest user message, as CompactionSplitIndex does", func(t *testing.T) {
		unbounded, ok := CompactionSplitIndex(turn, 1)
		if !ok || unbounded != 8 {
			t.Fatalf("CompactionSplitIndex = %d ok = %v, want 8", unbounded, ok)
		}
		if idx, ok := CompactionSplitIndexUpTo(turn, 1, len(turn)-1); !ok || idx != unbounded {
			t.Fatalf("an unbounded UpTo = %d ok = %v, want %d", idx, ok, unbounded)
		}
	})

	t.Run("the turns before the prompt are counted as before", func(t *testing.T) {
		// Two turns, the earlier one and the one in progress: keeping both leaves
		// nothing to fold, and the follow-up is not a third.
		if idx, ok := CompactionSplitIndexUpTo(turn, 2, open); ok {
			t.Fatalf("ok at %d: the follow-up was counted as a turn of its own", idx)
		}
	})

	t.Run("a hidden prompt leaves nothing to cut at", func(t *testing.T) {
		// The window opens with an in-turn row, one kept step and a follow-up the
		// user queued after it: the prompt lies before the window, so no user
		// message of the window is a boundary and the row with its steps is the
		// turn in progress.
		row := NewInTurnCompactionSummaryMessage("fix the build", "s", "m")
		msgs := concatMessages(turn[:8], []llm.Message{row}, stepOf("c3"), []llm.Message{at(userMsg("and the docs"), stopHookAt)}, stepOf("c4"))
		if idx, ok := CompactionSplitIndexUpTo(msgs, 1, open); ok {
			t.Fatalf("ok at %d: the follow-up was taken for a turn boundary", idx)
		}
		// The unbounded split is the bug this one exists to avoid: it cuts at the
		// follow-up and folds the row, and the prompt with it, into a plain summary.
		if idx, ok := CompactionSplitIndex(msgs, 1); !ok || msgs[idx].Content != "and the docs" {
			t.Fatalf("fixture: the unbounded split should land on the follow-up, got %d ok = %v", idx, ok)
		}
	})
}

// The turn's steps are counted from its opening prompt, so a follow-up in the
// middle of the turn does not make the steps before it vanish from the count.
func TestTurnStepsCountFromTheOpeningPrompt(t *testing.T) {
	turn := followUpTurn()
	opening := AnchorOf(turn[2])

	if got := TurnStepCount(turn, opening); got != 3 {
		t.Fatalf("steps = %d from the recorded prompt, want 3", got)
	}
	// Index 6 is the assistant message of the second step.
	if idx, ok := TurnStepSplitIndex(turn, 2, opening); !ok || idx != 6 {
		t.Fatalf("idx = %d ok = %v, want 6", idx, ok)
	}
	// Without a record the follow-up looks like the prompt and the first step is
	// lost from the count.
	if got := TurnStepCount(turn, TurnAnchor{}); got != 2 {
		t.Fatalf("steps = %d without a record, want 2 (counted after the queued follow-up)", got)
	}

	t.Run("a prompt hidden behind a row anchors at the row, follow-ups after it included", func(t *testing.T) {
		row := NewInTurnCompactionSummaryMessage("fix the build", "s", "m")
		msgs := concatMessages(turn[:5], []llm.Message{row}, stepOf("c2"), []llm.Message{at(userMsg("and the docs"), queuedAt)}, stepOf("c3"))
		// Window: row(5) c2(6-7) follow-up(8) c3(9-10): two steps, one on each
		// side of the follow-up.
		if got := TurnStepCount(msgs, opening); got != 2 {
			t.Fatalf("steps = %d, want 2", got)
		}
		if got := TurnStepCount(msgs, TurnAnchor{}); got != 1 {
			t.Fatalf("steps = %d without a record, want 1 (counted after the follow-up)", got)
		}
	})
}
