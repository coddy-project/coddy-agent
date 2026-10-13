package session

// The turn being answered and what a compaction inside it leaves behind (issue
// #490): which user message the turn opened with and which ones are follow-ups,
// the steps of the turn and where a fold may cut, the summary row a fold inside
// the turn writes, and what the readers of that row make of it - the replay a
// client is shown and the supervisor's digest.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// stepOf is one assistant message issuing a batch of tool calls with every
// result of that batch after it.
func stepOf(ids ...string) []llm.Message {
	call := llm.Message{Role: llm.RoleAssistant}
	var results []llm.Message
	for _, id := range ids {
		call.ToolCalls = append(call.ToolCalls, llm.ToolCall{ID: id, Name: "glob", InputJSON: "{}"})
		results = append(results, llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: "result " + id})
	}
	return append([]llm.Message{call}, results...)
}

func concatMessages(parts ...[]llm.Message) []llm.Message {
	var out []llm.Message
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// followUpMsg is a message the user queued while a turn ran, marked the way the
// agent marks it when it reads the queue.
func followUpMsg(text string) llm.Message {
	m := userMsg(text)
	m.FollowUp = true
	return m
}

// followUpTurn is a turn that opened with a prompt and went on through a queued
// follow-up and a Stop-hook follow-up, with earlier history in front of it:
//
//	0 earlier(u) 1 done(a) 2 opening(u) 3-4 step c1 5 queued(u) 6-7 step c2 8 stop(u) 9-10 step c3
func followUpTurn() []llm.Message {
	return concatMessages(
		[]llm.Message{userMsg("earlier"), assistantMsg("done"), userMsg("fix the build")},
		stepOf("c1"),
		[]llm.Message{followUpMsg("also keep the tests green")},
		stepOf("c2"),
		[]llm.Message{userMsg(StopHookPrefix + "run the linter")},
		stepOf("c3"),
	)
}

// withoutMarkers is msgs as a transcript written before the follow-up marker
// existed holds it.
func withoutMarkers(msgs []llm.Message) []llm.Message {
	out := append([]llm.Message(nil), msgs...)
	for i := range out {
		out[i].FollowUp = false
	}
	return out
}

// --- the opening prompt and the follow-ups ---

func TestOpeningPromptIndex(t *testing.T) {
	turn := followUpTurn()

	t.Run("the prompt is found after queued and Stop-hook follow-ups", func(t *testing.T) {
		if idx, ok := OpeningPromptIndex(turn); !ok || idx != 2 {
			t.Fatalf("idx = %d ok = %v, want 2", idx, ok)
		}
	})

	t.Run("a follow-up that repeats the prompt word for word is still a follow-up", func(t *testing.T) {
		// The same words written within the same second: neither the text nor the
		// time tells the two apart, only the marker does.
		const stamp = "2026-10-11T10:00:00Z"
		prompt, again := userMsg("continue"), followUpMsg("continue")
		prompt.CreatedAt, again.CreatedAt = stamp, stamp
		msgs := concatMessages(
			[]llm.Message{userMsg("older"), assistantMsg("a"), prompt}, stepOf("c1"),
			[]llm.Message{again}, stepOf("c2"),
		)
		if idx, ok := OpeningPromptIndex(msgs); !ok || idx != 2 {
			t.Fatalf("idx = %d ok = %v, want 2: the prompt, not the follow-up with the same text and time", idx, ok)
		}
	})

	t.Run("without markers the last user message counts", func(t *testing.T) {
		// A transcript written before the marker existed cannot tell a queued
		// follow-up from the prompt: its last user message opens the turn.
		if idx, ok := OpeningPromptIndex(withoutMarkers(turn)); !ok || idx != 5 {
			t.Fatalf("idx = %d ok = %v, want 5 (the unmarked queued follow-up)", idx, ok)
		}
		// A Stop-hook follow-up is known by its prefix and never counts, marker or not.
		plain := concatMessages([]llm.Message{userMsg("old"), assistantMsg("a"), userMsg("the prompt")}, stepOf("c1"),
			[]llm.Message{userMsg(StopHookPrefix + "again")}, stepOf("c2"))
		if idx, ok := OpeningPromptIndex(plain); !ok || idx != 2 {
			t.Fatalf("idx = %d ok = %v, want 2: a Stop-hook follow-up is never the prompt", idx, ok)
		}
	})

	t.Run("a woken turn's and a goal turn's first message is its prompt", func(t *testing.T) {
		wake := userMsg("background task finished")
		wake.BackgroundWake = &llm.BackgroundWake{}
		goal := userMsg("SUPERVISOR: keep working toward the goal")
		goal.GoalTurn = &llm.GoalTurn{Kind: "continue", Objective: "fix the parser"}
		for _, opening := range []llm.Message{wake, goal} {
			msgs := concatMessages([]llm.Message{userMsg("old"), assistantMsg("a"), opening}, stepOf("c1"), []llm.Message{followUpMsg("and the docs")})
			if idx, ok := OpeningPromptIndex(msgs); !ok || idx != 2 {
				t.Fatalf("%q: idx = %d ok = %v, want 2", opening.Content, idx, ok)
			}
		}
	})

	t.Run("a summary row never counts", func(t *testing.T) {
		if idx, ok := OpeningPromptIndex([]llm.Message{NewCompactionSummaryMessage("only a summary", "m"), assistantMsg("a")}); ok {
			t.Fatalf("idx = %d for a history whose only user-role row is a summary", idx)
		}
		// An in-turn row written after the prompt is no prompt either.
		msgs := concatMessages(turn, []llm.Message{NewInTurnCompactionSummaryMessage("fix the build", "s", "m")}, stepOf("c4"))
		if idx, ok := OpeningPromptIndex(msgs); !ok || idx != 2 {
			t.Fatalf("idx = %d ok = %v, want 2: the prompt in front of the row", idx, ok)
		}
	})

	t.Run("no user message means no prompt", func(t *testing.T) {
		for name, msgs := range map[string][]llm.Message{
			"an empty history":     nil,
			"assistant rows alone": {assistantMsg("a"), assistantMsg("b")},
			"follow-ups alone":     {followUpMsg("x"), assistantMsg("a"), userMsg(StopHookPrefix + "y")},
		} {
			if idx, ok := OpeningPromptIndex(msgs); ok {
				t.Errorf("%s: idx = %d, want no prompt", name, idx)
			}
		}
	})
}

// The marker is persisted with the transcript: a session loaded again after a
// restart still tells the prompt from the follow-ups.
func TestFollowUpMarkerSurvivesARestart(t *testing.T) {
	t.Run("the field round-trips through JSON and is left out when false", func(t *testing.T) {
		data, err := json.Marshal(followUpMsg("keep the tests green"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"follow_up":true`) {
			t.Fatalf("encoded follow-up = %s, want the follow_up field", data)
		}
		var back llm.Message
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatal(err)
		}
		if !back.FollowUp || back.Content != "keep the tests green" || back.Role != llm.RoleUser {
			t.Fatalf("decoded follow-up = %+v", back)
		}

		data, err = json.Marshal(userMsg("fix the build"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "follow_up") {
			t.Fatalf("encoded prompt = %s, want no follow_up field", data)
		}
		back = llm.Message{}
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatal(err)
		}
		if back.FollowUp {
			t.Fatal("a message encoded without the field decoded as a follow-up")
		}
	})

	t.Run("a decoded transcript finds the same opening prompt", func(t *testing.T) {
		data, err := json.Marshal(followUpTurn())
		if err != nil {
			t.Fatal(err)
		}
		var back []llm.Message
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatal(err)
		}
		if idx, ok := OpeningPromptIndex(back); !ok || idx != 2 {
			t.Fatalf("idx = %d ok = %v after a round trip, want 2", idx, ok)
		}
	})

	t.Run("a saved and reloaded session finds the same opening prompt", func(t *testing.T) {
		root := t.TempDir()
		st := &State{ID: "sess_follow_up_restart", CWD: root, SessionDir: filepath.Join(root, "sessions", "sess_follow_up_restart")}
		if err := os.MkdirAll(st.SessionDir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, m := range followUpTurn() {
			st.AddMessage(m)
		}
		store := &FileStore{Root: filepath.Join(root, "sessions")}
		if err := store.Save(st); err != nil {
			t.Fatal(err)
		}
		snap, err := store.ReadSnapshot(st.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !snap.Messages[5].FollowUp {
			t.Fatalf("the queued follow-up lost its marker on reload: %+v", snap.Messages[5])
		}
		if idx, ok := OpeningPromptIndex(snap.Messages); !ok || idx != 2 {
			t.Fatalf("idx = %d ok = %v after a reload, want 2", idx, ok)
		}
	})
}

// --- the boundaries a compaction cuts at ---

// Only the user messages up to the opening prompt are turn boundaries for the
// split a turn in progress is compacted at: a follow-up inside the turn is not.
func TestCompactionSplitIndexUpToIgnoresFollowUps(t *testing.T) {
	turn := followUpTurn()
	open, ok := OpeningPromptIndex(turn)
	if !ok || open != 2 {
		t.Fatalf("fixture: opening = %d ok = %v", open, ok)
	}

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
		msgs := concatMessages(turn[:8], []llm.Message{row}, stepOf("c3"), []llm.Message{followUpMsg("and the docs")}, stepOf("c4"))
		open, ok := OpeningPromptIndex(msgs)
		if !ok || open != 2 {
			t.Fatalf("fixture: opening = %d ok = %v, want 2", open, ok)
		}
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

// After an in-turn fold the window opens with a summary row and the steps that
// were kept. Without counting that row as the start of a turn, a new prompt
// would find "one user turn" in the window and the old row and its steps could
// never be folded.
func TestCompactionSplitIndexTreatsAnInTurnSummaryAsATurn(t *testing.T) {
	inTurn := NewInTurnCompactionSummaryMessage("do it", "earlier steps", "m")
	remainder := concatMessages([]llm.Message{inTurn}, stepOf("c1"), stepOf("c2"), []llm.Message{assistantMsg("done")})

	t.Run("a new prompt splits at itself and folds the row with its steps", func(t *testing.T) {
		msgs := concatMessages(remainder, []llm.Message{userMsg("next"), assistantMsg("ok")})
		idx, ok := CompactionSplitIndex(msgs, 1)
		if !ok || idx != len(remainder) {
			t.Fatalf("idx = %d ok = %v, want %d", idx, ok, len(remainder))
		}
	})

	t.Run("the window alone is one turn, nothing to fold at keep one", func(t *testing.T) {
		if idx, ok := CompactionSplitIndex(remainder, 1); ok {
			t.Fatalf("ok at %d: the remainder is the turn being answered", idx)
		}
	})

	t.Run("the manual floor folds the row and its steps", func(t *testing.T) {
		idx, ok := CompactionSplitIndex(remainder, 0)
		if !ok || idx != len(remainder) {
			t.Fatalf("idx = %d ok = %v, want %d", idx, ok, len(remainder))
		}
	})

	t.Run("a summary a compact_context call wrote counts the same way", func(t *testing.T) {
		regular := concatMessages([]llm.Message{NewCompactionSummaryMessage("everything before", "m")}, stepOf("c1"))
		msgs := concatMessages(regular, []llm.Message{userMsg("next")}, stepOf("n1"))
		if idx, ok := CompactionSplitIndex(msgs, 1); !ok || idx != len(regular) {
			t.Fatalf("idx = %d ok = %v, want %d", idx, ok, len(regular))
		}
	})

	t.Run("a regular summary followed by a user message behaves as before", func(t *testing.T) {
		msgs := concatMessages([]llm.Message{NewCompactionSummaryMessage("older", "m")}, exchangeMessages(2))
		if _, ok := CompactionSplitIndex(msgs, 2); ok {
			t.Fatal("two user turns after a regular summary are not more than keep 2")
		}
		if idx, ok := CompactionSplitIndex(msgs, 1); !ok || idx != 3 {
			t.Fatalf("idx = %d ok = %v, want 3", idx, ok)
		}
	})
}

// --- the steps of the turn ---

// A turn is its prompt followed by steps. The split always lands on the
// assistant message that opens a step, so a batch of parallel calls is never
// cut between the call and its results.
func TestTurnStepSplitIndex(t *testing.T) {
	// u, [a1 t t], [a2 t], [a3 t], [a4 t t t]
	turn := concatMessages(
		[]llm.Message{userMsg("do it")},
		stepOf("c1", "c2"), stepOf("c3"), stepOf("c4"), stepOf("c5", "c6", "c7"),
	)
	// Absolute indexes of the last three assistant messages.
	const a2, a3, a4 = 4, 6, 8

	t.Run("keeps the last two steps and splits at the assistant message", func(t *testing.T) {
		idx, ok := TurnStepSplitIndex(turn, 2)
		if !ok || idx != a3 {
			t.Fatalf("idx = %d ok = %v, want %d", idx, ok, a3)
		}
		if turn[idx].Role != llm.RoleAssistant {
			t.Fatalf("the tail starts at %s, want the assistant message that opens a step", turn[idx].Role)
		}
	})

	t.Run("a parallel batch is never split at any kept count", func(t *testing.T) {
		for keep := 1; keep <= 3; keep++ {
			idx, ok := TurnStepSplitIndex(turn, keep)
			if !ok {
				t.Fatalf("keep %d: ok = false", keep)
			}
			if turn[idx].Role != llm.RoleAssistant {
				t.Fatalf("keep %d: split at a %s message", keep, turn[idx].Role)
			}
			// Every result in the tail answers a call that is also in the tail.
			calls := map[string]bool{}
			for _, m := range turn[idx:] {
				for _, c := range m.ToolCalls {
					calls[c.ID] = true
				}
				if m.Role == llm.RoleTool && !calls[m.ToolCallID] {
					t.Fatalf("keep %d: tail holds a result of %s whose call was folded away", keep, m.ToolCallID)
				}
			}
			// And none in the head answers a call in the tail.
			for _, m := range turn[:idx] {
				if m.Role == llm.RoleTool && calls[m.ToolCallID] {
					t.Fatalf("keep %d: head holds a result of %s whose call is in the tail", keep, m.ToolCallID)
				}
			}
		}
	})

	t.Run("each kept count lands on its own step", func(t *testing.T) {
		for keep, want := range map[int]int{1: a4, 2: a3, 3: a2} {
			if idx, ok := TurnStepSplitIndex(turn, keep); !ok || idx != want {
				t.Fatalf("keep %d: idx = %d ok = %v, want %d", keep, idx, ok, want)
			}
		}
	})

	t.Run("a turn with no more steps than kept has nothing to fold", func(t *testing.T) {
		for _, keep := range []int{4, 5, 100} {
			if idx, ok := TurnStepSplitIndex(turn, keep); ok {
				t.Fatalf("keep %d: ok = true at %d for a four-step turn", keep, idx)
			}
		}
	})

	t.Run("keeping nothing folds the whole turn", func(t *testing.T) {
		if idx, ok := TurnStepSplitIndex(turn, 0); !ok || idx != len(turn) {
			t.Fatalf("idx = %d ok = %v, want %d", idx, ok, len(turn))
		}
		if idx, ok := TurnStepSplitIndex(turn, -3); !ok || idx != len(turn) {
			t.Fatalf("a negative count must mean zero: idx = %d ok = %v", idx, ok)
		}
		if _, ok := TurnStepSplitIndex([]llm.Message{userMsg("only a prompt")}, 0); ok {
			t.Fatal("a prompt with no step at all has nothing to fold")
		}
	})

	t.Run("steps of earlier turns are not counted", func(t *testing.T) {
		msgs := concatMessages(
			[]llm.Message{userMsg("first")}, stepOf("e1"), stepOf("e2"), []llm.Message{assistantMsg("done")},
			[]llm.Message{userMsg("second")}, stepOf("c1"), stepOf("c2"),
		)
		// u(0) a(1) t(2) a(3) t(4) a(5) u(6) a(7) t(8) a(9) t(10): the second
		// turn has two steps, at 7 and 9.
		if idx, ok := TurnStepSplitIndex(msgs, 1); !ok || idx != 9 {
			t.Fatalf("idx = %d ok = %v, want 9", idx, ok)
		}
		if _, ok := TurnStepSplitIndex(msgs, 2); ok {
			t.Fatal("the first turn's steps were counted for the second")
		}
	})

	t.Run("a plan document row between a call and its results is no step", func(t *testing.T) {
		plan := llm.Message{Role: llm.RoleAssistant, PlanDocument: &llm.PlanDocumentSnapshot{Slug: "p", Content: "plan"}}
		call := stepOf("c1", "c2")
		// u(0) a(1) plan(2) t(3) t(4) a(5) t(6): the plan row sits inside the
		// first batch, so the only boundaries are 1 and 5.
		msgs := concatMessages([]llm.Message{userMsg("plan it")}, call[:1], []llm.Message{plan}, call[1:], stepOf("c3"))
		if idx, ok := TurnStepSplitIndex(msgs, 1); !ok || idx != 5 {
			t.Fatalf("idx = %d ok = %v, want 5", idx, ok)
		}
		if _, ok := TurnStepSplitIndex(msgs, 2); ok {
			t.Fatal("the plan row was counted as a step")
		}
	})

	t.Run("no prompt and no summary means no turn", func(t *testing.T) {
		msgs := concatMessages(stepOf("c1"), stepOf("c2"))
		if _, ok := TurnStepSplitIndex(msgs, 1); ok {
			t.Fatal("a window with neither a prompt nor a summary row has no turn to fold")
		}
		if _, ok := TurnStepSplitIndex(nil, 0); ok {
			t.Fatal("an empty history has no turn")
		}
	})
}

// A fold that took the prompt away (an in-turn one, or a compact_context call)
// leaves a summary row at the start of the window, followed by the steps that
// were kept. That row stands for the turn's prompt.
func TestTurnStepSplitIndexStartsAtTheSummaryWhenThePromptIsHidden(t *testing.T) {
	for name, row := range map[string]llm.Message{
		"an in-turn row": NewInTurnCompactionSummaryMessage("do it", "what was done", "m"),
		"a plain row":    NewCompactionSummaryMessage("what was done", "m"),
	} {
		t.Run(name, func(t *testing.T) {
			msgs := concatMessages(
				[]llm.Message{userMsg("do it")}, stepOf("old1"), stepOf("old2"),
				[]llm.Message{row},
				stepOf("c1"), stepOf("c2"), stepOf("c3"),
			)
			// u(0) a(1) t(2) a(3) t(4) | summary(5) a(6) t(7) a(8) t(9) a(10) t(11):
			// the window starts at 5 and holds three steps.
			if idx, ok := TurnStepSplitIndex(msgs, 2); !ok || idx != 8 {
				t.Fatalf("idx = %d ok = %v, want 8", idx, ok)
			}
			if _, ok := TurnStepSplitIndex(msgs, 3); ok {
				t.Fatal("the steps hidden behind the summary were counted")
			}

			// A real prompt after the summary row starts the turn instead.
			withNew := concatMessages(msgs, []llm.Message{userMsg("next")}, stepOf("n1"), stepOf("n2"))
			if idx, ok := TurnStepSplitIndex(withNew, 1); !ok || idx != len(withNew)-2 {
				t.Fatalf("idx = %d ok = %v, want %d", idx, ok, len(withNew)-2)
			}
			if _, ok := TurnStepSplitIndex(withNew, 2); ok {
				t.Fatal("the steps before the new prompt were counted as steps of its turn")
			}
		})
	}
}

// TurnStepCount counts the steps TurnStepSplitIndex counts back through.
func TestTurnStepCount(t *testing.T) {
	plan := llm.Message{Role: llm.RoleAssistant, PlanDocument: &llm.PlanDocumentSnapshot{Slug: "p", Content: "plan"}}
	cases := []struct {
		name string
		msgs []llm.Message
		want int
	}{
		{name: "no history", msgs: nil, want: 0},
		{name: "a prompt alone", msgs: []llm.Message{userMsg("p")}, want: 0},
		{name: "four steps of a turn", msgs: concatMessages([]llm.Message{userMsg("p")}, stepOf("a", "b"), stepOf("c"), stepOf("d"), stepOf("e")), want: 4},
		{name: "earlier turns are not the turn", msgs: concatMessages([]llm.Message{userMsg("p")}, stepOf("a"), stepOf("b"), []llm.Message{userMsg("q")}, stepOf("c")), want: 1},
		{name: "a plan row is no step", msgs: concatMessages([]llm.Message{userMsg("p")}, stepOf("a"), []llm.Message{plan}), want: 1},
		{name: "no prompt and no summary", msgs: stepOf("a"), want: 0},
		{name: "the summary row stands for the prompt", msgs: concatMessages([]llm.Message{NewInTurnCompactionSummaryMessage("p", "s", "m")}, stepOf("a"), stepOf("b")), want: 2},
		{name: "steps after a follow-up belong to the turn", msgs: followUpTurn(), want: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TurnStepCount(tc.msgs); got != tc.want {
				t.Fatalf("TurnStepCount = %d, want %d", got, tc.want)
			}
			// The two functions agree: the split index exists exactly for the
			// kept counts below the number of steps.
			for keep := 0; keep <= tc.want+2; keep++ {
				_, ok := TurnStepSplitIndex(tc.msgs, keep)
				if ok != (keep < tc.want) {
					t.Fatalf("keep %d: ok = %v with %d steps", keep, ok, tc.want)
				}
			}
		})
	}
}

// The turn's steps are counted from its opening prompt, so a follow-up in the
// middle of the turn does not make the steps before it vanish from the count.
func TestTurnStepsCountFromTheOpeningPrompt(t *testing.T) {
	turn := followUpTurn()

	if got := TurnStepCount(turn); got != 3 {
		t.Fatalf("steps = %d from the opening prompt, want 3", got)
	}
	// Index 6 is the assistant message of the second step.
	if idx, ok := TurnStepSplitIndex(turn, 2); !ok || idx != 6 {
		t.Fatalf("idx = %d ok = %v, want 6", idx, ok)
	}
	// Without markers the queued follow-up looks like the prompt and the first
	// step is lost from the count.
	if got := TurnStepCount(withoutMarkers(turn)); got != 2 {
		t.Fatalf("steps = %d without markers, want 2 (counted after the queued follow-up)", got)
	}

	t.Run("a prompt hidden behind a row starts the turn at the row, follow-ups after it included", func(t *testing.T) {
		row := NewInTurnCompactionSummaryMessage("fix the build", "s", "m")
		msgs := concatMessages(turn[:5], []llm.Message{row}, stepOf("c2"), []llm.Message{followUpMsg("and the docs")}, stepOf("c3"))
		// Window: row(5) c2(6-7) follow-up(8) c3(9-10): two steps, one on each
		// side of the follow-up.
		if got := TurnStepCount(msgs); got != 2 {
			t.Fatalf("steps = %d, want 2", got)
		}
		if got := TurnStepCount(withoutMarkers(msgs)); got != 1 {
			t.Fatalf("steps = %d without markers, want 1 (counted after the follow-up)", got)
		}
	})
}

// --- the in-turn summary row ---

func TestNewInTurnCompactionSummaryMessage(t *testing.T) {
	m := NewInTurnCompactionSummaryMessage("fix the build", "  steps one to four  ", "prov/model")
	if m.Role != llm.RoleUser || !m.CompactionSummary || m.Model != "prov/model" || m.CreatedAt == "" {
		t.Fatalf("not shaped like a summary row: %+v", m)
	}
	// The prompt verbatim, then one preamble that names no author: a goal turn's
	// instruction and a background wake's notice say who wrote them themselves.
	const want = "fix the build\n\n" +
		"The text above is the message this turn started with, verbatim. " +
		"Coddy compacted the conversation before this point, " +
		"including the earlier steps of the work on it. Summary of the compacted part:\n\n" +
		"steps one to four"
	if m.Content != want {
		t.Fatalf("content layout changed:\n got: %q\nwant: %q", m.Content, want)
	}
	// The web UI renders what follows the last words of the preamble.
	if _, body, ok := strings.Cut(m.Content, "Summary of the compacted part:"); !ok || strings.TrimSpace(body) != "steps one to four" {
		t.Fatalf("the body after the UI's cut is %q", body)
	}
}

func TestSplitInTurnSummary(t *testing.T) {
	t.Run("an in-turn row splits into the opening message and the summary", func(t *testing.T) {
		row := NewInTurnCompactionSummaryMessage("fix the build\n\nthen explain why", "  steps one to four  ", "m")
		prompt, summary, ok := SplitInTurnSummary(row.Content)
		if !ok || prompt != "fix the build\n\nthen explain why" || summary != "steps one to four" {
			t.Fatalf("got %q / %q ok = %v", prompt, summary, ok)
		}
	})

	t.Run("a plain row is not split, even when its summary quotes the preamble", func(t *testing.T) {
		quoting := NewCompactionSummaryMessage("the row said:\n\n"+inTurnSummaryPreamble+"and more", "m")
		quotingRow := NewCompactionSummaryMessage("the row said:\n\n"+NewInTurnCompactionSummaryMessage("p", "s", "m").Content, "m")
		for _, content := range []string{NewCompactionSummaryMessage("a summary", "m").Content, quoting.Content, quotingRow.Content} {
			if prompt, summary, ok := SplitInTurnSummary(content); ok {
				t.Fatalf("split a plain row into %q / %q", prompt, summary)
			}
		}
	})

	t.Run("text without the preamble is not a row", func(t *testing.T) {
		for _, content := range []string{
			"",
			"fix the build",
			"fix the build\n\nThe text above is the message this turn started with",
			// What a replay shows: the preamble with no opening message in front.
			SummaryReplayText(NewInTurnCompactionSummaryMessage("p", "s", "m").Content),
		} {
			if _, _, ok := SplitInTurnSummary(content); ok {
				t.Fatalf("%q was taken for an in-turn row", content)
			}
		}
	})

	t.Run("the first occurrence of the preamble is the boundary", func(t *testing.T) {
		// A summary that quotes the preamble keeps the quote.
		row := NewInTurnCompactionSummaryMessage("the request", "it said:\n\n"+inTurnSummaryPreamble+"tail", "m")
		prompt, summary, ok := SplitInTurnSummary(row.Content)
		if !ok || prompt != "the request" || !strings.Contains(summary, "it said:") || !strings.HasSuffix(summary, "tail") {
			t.Fatalf("got %q / %q ok = %v", prompt, summary, ok)
		}
		// A request that quotes it is the one input that is misread: what follows
		// the quote is taken for the summary.
		quoting := NewInTurnCompactionSummaryMessage("see:\n\n"+inTurnSummaryPreamble+"and then fix it", "s", "m")
		prompt, _, ok = SplitInTurnSummary(quoting.Content)
		if !ok || prompt != "see:" {
			t.Fatalf("got %q ok = %v: the documented misreading changed", prompt, ok)
		}
	})
}

func TestSummaryReplayText(t *testing.T) {
	plain := NewCompactionSummaryMessage("a summary", "m").Content
	if got := SummaryReplayText(plain); got != plain {
		t.Fatalf("a plain row must be replayed as stored, got %q", got)
	}
	row := NewInTurnCompactionSummaryMessage("fix the build", "steps one to four", "m")
	got := SummaryReplayText(row.Content)
	if got != inTurnSummaryPreamble+"steps one to four" || strings.Contains(got, "fix the build") {
		t.Fatalf("an in-turn row must be replayed from its preamble on, got %q", got)
	}
	if again := SummaryReplayText(got); again != got {
		t.Fatalf("the replay text read again changed: %q", again)
	}
}

// chunkRecorder keeps the text of the user message chunks a replay sends.
type chunkRecorder struct{ users []string }

func (r *chunkRecorder) SendSessionUpdate(_ string, u interface{}) error {
	if c, ok := u.(acp.MessageChunkUpdate); ok && c.SessionUpdate == "user_message_chunk" {
		r.users = append(r.users, c.Content.Text)
	}
	return nil
}

func (r *chunkRecorder) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}

func (r *chunkRecorder) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

// A loaded session replays an in-turn row from its preamble on: the opening
// message in front of it was replayed at its own place, and a second copy would
// show the prompt twice. A plain row replays as stored.
func TestReplayShowsAnInTurnRowFromItsPreambleOn(t *testing.T) {
	const prompt = "Fix the parser and keep the API."
	msgs := concatMessages(
		[]llm.Message{userMsg(prompt)}, stepOf("c1"),
		[]llm.Message{NewInTurnCompactionSummaryMessage(prompt, "the worker changed two functions", "m")}, stepOf("c2"),
		[]llm.Message{followUpMsg("and the docs"), NewCompactionSummaryMessage("a plain row", "m")},
	)
	rec := &chunkRecorder{}
	m := &Manager{server: rec}
	if err := m.replayConversation("sess", msgs, ""); err != nil {
		t.Fatal(err)
	}
	if len(rec.users) != 4 {
		t.Fatalf("replayed %d user rows, want 4: %q", len(rec.users), rec.users)
	}
	if rec.users[0] != prompt {
		t.Fatalf("the prompt must be replayed at its own place, got %q", rec.users[0])
	}
	if want := inTurnSummaryPreamble + "the worker changed two functions"; rec.users[1] != want {
		t.Fatalf("the in-turn row replays as %q, want %q", rec.users[1], want)
	}
	if got := strings.Count(strings.Join(rec.users, "\n"), prompt); got != 1 {
		t.Fatalf("the prompt is replayed %d times, want once: %q", got, rec.users)
	}
	if rec.users[2] != "and the docs" {
		t.Fatalf("a follow-up replays as %q, want its text", rec.users[2])
	}
	if !strings.HasPrefix(rec.users[3], "The earlier conversation was compacted.") {
		t.Fatalf("a plain row must replay as stored, got %q", rec.users[3])
	}
}

// --- the supervisor's digest ---

// openingLabel is the label the digest gives the message in front of the
// summary of an in-turn row, whoever wrote it.
const openingLabel = "[the message this turn started with] "

func digestOf(msgs ...llm.Message) string {
	goal, _ := NewGoal("fix the parser")
	all := append([]llm.Message{{Role: llm.RoleUser, Content: "kickoff", GoalTurn: &llm.GoalTurn{Kind: "kickoff", Objective: "fix the parser"}}}, msgs...)
	return buildGoalDigest(all, goal, false)
}

// An in-turn row is two steps of the digest: the opening message in front of it,
// verbatim, and the worker's account of the steps it folded.
func TestGoalDigestSplitsAnInTurnRowIntoTheOpeningMessageAndTheWorkerSummary(t *testing.T) {
	row := NewInTurnCompactionSummaryMessage("Fix the parser and keep the API.", "the worker read parse.go and changed two functions", "m")
	d := digestOf(row)
	for _, want := range []string{
		openingLabel + "Fix the parser and keep the API.\n",
		"[summary of earlier steps of this request, written by the worker: claims, not evidence] the worker read parse.go and changed two functions\n",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("digest lacks %q:\n%s", want, d)
		}
	}
	// The opening message may be a supervisor's instruction or a wake notice as
	// well as the operator's request: the row does not say, and neither does the
	// digest.
	if strings.Contains(d, "[operator] ") {
		t.Errorf("the opening message was labelled as the operator's:\n%s", d)
	}
	if strings.Contains(d, "summary of earlier turns") || strings.Contains(d, "The text above is") {
		t.Errorf("the row was labelled as a plain summary or its preamble leaked:\n%s", d)
	}

	t.Run("a plain row keeps its label", func(t *testing.T) {
		d := digestOf(NewCompactionSummaryMessage("earlier turns went well", "m"))
		if !strings.Contains(d, "[summary of earlier turns, written by the worker: claims, not evidence] The earlier conversation was compacted.") {
			t.Errorf("digest:\n%s", d)
		}
		if strings.Contains(d, openingLabel) {
			t.Errorf("a plain row was split:\n%s", d)
		}
	})
}

func TestGoalDigestClipsTheRequestAndTheSummaryOnTheirOwn(t *testing.T) {
	t.Run("a long summary leaves the request whole", func(t *testing.T) {
		d := digestOf(NewInTurnCompactionSummaryMessage("SHORT-REQUEST-MARKER", strings.Repeat("claim. ", 1000), "m"))
		if !strings.Contains(d, openingLabel+"SHORT-REQUEST-MARKER\n") {
			t.Errorf("the request did not survive a long summary:\n%s", firstOf(d, 600))
		}
	})
	t.Run("a long request leaves the summary whole", func(t *testing.T) {
		d := digestOf(NewInTurnCompactionSummaryMessage(strings.Repeat("pasted log line. ", 400), "SHORT-SUMMARY-MARKER", "m"))
		if !strings.Contains(d, "claims, not evidence] SHORT-SUMMARY-MARKER\n") {
			t.Errorf("the summary was clipped away with a long request:\n%s", firstOf(d, 600))
		}
		found := false
		for _, line := range strings.Split(d, "\n") {
			if !strings.HasPrefix(line, openingLabel) {
				continue
			}
			found = true
			if !strings.HasSuffix(line, "...") || len([]rune(line)) > len([]rune(openingLabel))+goalDigestText+3 {
				t.Errorf("the request line was not clipped to %d runes: %d runes", goalDigestText, len([]rune(line)))
			}
		}
		if !found {
			t.Errorf("the digest has no line for the request:\n%s", firstOf(d, 600))
		}
	})
}

func firstOf(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// goalStartIndex for the implicit goal of supervisor.enable is the latest
// request. A fold inside the turn inserts a user-role summary row after the
// prompt, and that row is no request.
func TestGoalStartIndexSkipsASummaryRow(t *testing.T) {
	prompt := llm.Message{Role: llm.RoleUser, Content: "Fix the parser."}
	row := NewInTurnCompactionSummaryMessage("Fix the parser.", "the worker read parse.go", "m")
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "an older request"}, {Role: llm.RoleAssistant, Content: "done"},
		prompt, {Role: llm.RoleAssistant, Content: "reading"}, {Role: llm.RoleTool, Content: "file"},
		row, {Role: llm.RoleAssistant, Content: "editing"},
	}

	t.Run("an in-turn row after the prompt", func(t *testing.T) {
		if got := goalStartIndex(msgs, GoalState{}, true); got != 2 {
			t.Fatalf("goalStartIndex = %d, want 2 (the prompt, not the summary row at 5)", got)
		}
	})

	t.Run("a plain row after the prompt", func(t *testing.T) {
		plain := append(append([]llm.Message(nil), msgs[:5]...), NewCompactionSummaryMessage("earlier turns", "m"), msgs[6])
		if got := goalStartIndex(plain, GoalState{}, true); got != 2 {
			t.Fatalf("goalStartIndex = %d, want 2", got)
		}
	})

	t.Run("a goal or wake turn message still does not count", func(t *testing.T) {
		woken := append(append([]llm.Message(nil), msgs...),
			llm.Message{Role: llm.RoleUser, Content: "wake", BackgroundWake: &llm.BackgroundWake{}},
			llm.Message{Role: llm.RoleUser, Content: "go on", GoalTurn: &llm.GoalTurn{Kind: "continue"}})
		if got := goalStartIndex(woken, GoalState{}, true); got != 2 {
			t.Fatalf("goalStartIndex = %d, want 2", got)
		}
	})

	t.Run("nothing but summary rows falls back to the start", func(t *testing.T) {
		if got := goalStartIndex([]llm.Message{row, {Role: llm.RoleAssistant, Content: "x"}}, GoalState{}, true); got != 0 {
			t.Fatalf("goalStartIndex = %d, want 0", got)
		}
	})
}
