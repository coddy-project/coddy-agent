package session

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// toolStep is one assistant message issuing a batch of tool calls with every
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

// A turn is its prompt followed by steps. The split always lands on the
// assistant message that opens a step, so a batch of parallel calls is never
// cut between the call and its results.
func TestTurnStepSplitIndex(t *testing.T) {
	// u, [a1 t t], [a2 t], [a3 t], [a4 t t t]
	turn := concatMessages(
		[]llm.Message{userMsg("do it")},
		stepOf("c1", "c2"), stepOf("c3"), stepOf("c4"), stepOf("c5", "c6", "c7"),
	)
	// Absolute indexes of the four assistant messages.
	const a2, a3, a4 = 4, 6, 8

	t.Run("keeps the last two steps and splits at the assistant message", func(t *testing.T) {
		idx, ok := TurnStepSplitIndex(turn, 2, TurnAnchor{})
		if !ok || idx != a3 {
			t.Fatalf("idx = %d ok = %v, want %d", idx, ok, a3)
		}
		if turn[idx].Role != llm.RoleAssistant {
			t.Fatalf("the tail starts at %s, want the assistant message that opens a step", turn[idx].Role)
		}
	})

	t.Run("a parallel batch is never split at any kept count", func(t *testing.T) {
		for keep := 1; keep <= 3; keep++ {
			idx, ok := TurnStepSplitIndex(turn, keep, TurnAnchor{})
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
			if idx, ok := TurnStepSplitIndex(turn, keep, TurnAnchor{}); !ok || idx != want {
				t.Fatalf("keep %d: idx = %d ok = %v, want %d", keep, idx, ok, want)
			}
		}
	})

	t.Run("a turn with no more steps than kept has nothing to fold", func(t *testing.T) {
		for _, keep := range []int{4, 5, 100} {
			if idx, ok := TurnStepSplitIndex(turn, keep, TurnAnchor{}); ok {
				t.Fatalf("keep %d: ok = true at %d for a four-step turn", keep, idx)
			}
		}
	})

	t.Run("keeping nothing folds the whole turn", func(t *testing.T) {
		if idx, ok := TurnStepSplitIndex(turn, 0, TurnAnchor{}); !ok || idx != len(turn) {
			t.Fatalf("idx = %d ok = %v, want %d", idx, ok, len(turn))
		}
		if idx, ok := TurnStepSplitIndex(turn, -3, TurnAnchor{}); !ok || idx != len(turn) {
			t.Fatalf("a negative count must mean zero: idx = %d ok = %v", idx, ok)
		}
		if _, ok := TurnStepSplitIndex([]llm.Message{userMsg("only a prompt")}, 0, TurnAnchor{}); ok {
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
		if idx, ok := TurnStepSplitIndex(msgs, 1, TurnAnchor{}); !ok || idx != 9 {
			t.Fatalf("idx = %d ok = %v, want 9", idx, ok)
		}
		if _, ok := TurnStepSplitIndex(msgs, 2, TurnAnchor{}); ok {
			t.Fatal("the first turn's steps were counted for the second")
		}
	})

	t.Run("a plan document row between a call and its results is no step", func(t *testing.T) {
		plan := llm.Message{Role: llm.RoleAssistant, PlanDocument: &llm.PlanDocumentSnapshot{Slug: "p", Content: "plan"}}
		call := stepOf("c1", "c2")
		// u(0) a(1) plan(2) t(3) t(4) a(5) t(6): the plan row sits inside the
		// first batch, so the only boundaries are 1 and 5.
		msgs := concatMessages([]llm.Message{userMsg("plan it")}, call[:1], []llm.Message{plan}, call[1:], stepOf("c3"))
		if idx, ok := TurnStepSplitIndex(msgs, 1, TurnAnchor{}); !ok || idx != 5 {
			t.Fatalf("idx = %d ok = %v, want 5", idx, ok)
		}
		if _, ok := TurnStepSplitIndex(msgs, 2, TurnAnchor{}); ok {
			t.Fatal("the plan row was counted as a step")
		}
	})

	t.Run("no prompt and no summary means no turn", func(t *testing.T) {
		msgs := concatMessages(stepOf("c1"), stepOf("c2"))
		if _, ok := TurnStepSplitIndex(msgs, 1, TurnAnchor{}); ok {
			t.Fatal("a window with neither a prompt nor a summary row has no turn to fold")
		}
		if _, ok := TurnStepSplitIndex(nil, 0, TurnAnchor{}); ok {
			t.Fatal("an empty history has no turn")
		}
	})
}

// A fold that took the prompt away (an in-turn one, or a compact_context call)
// leaves a summary row at the start of the window, followed by the steps that
// were kept. That row stands for the turn's prompt.
func TestTurnStepSplitIndexAnchorsAtTheSummaryWhenThePromptIsHidden(t *testing.T) {
	prefixed := NewInTurnCompactionSummaryMessage(TurnSourceUser, "do it", "what was done", "m")
	msgs := concatMessages(
		[]llm.Message{userMsg("do it")}, stepOf("old1"), stepOf("old2"),
		[]llm.Message{prefixed},
		stepOf("c1"), stepOf("c2"), stepOf("c3"),
	)
	// u(0) a(1) t(2) a(3) t(4) | summary(5) a(6) t(7) a(8) t(9) a(10) t(11):
	// the window starts at 5 and holds three steps.
	if idx, ok := TurnStepSplitIndex(msgs, 2, TurnAnchor{}); !ok || idx != 8 {
		t.Fatalf("idx = %d ok = %v, want 8", idx, ok)
	}
	if _, ok := TurnStepSplitIndex(msgs, 3, TurnAnchor{}); ok {
		t.Fatal("the steps hidden behind the summary were counted")
	}

	// A real prompt after the summary row is the anchor instead.
	withNew := concatMessages(msgs, []llm.Message{userMsg("next")}, stepOf("n1"), stepOf("n2"))
	if idx, ok := TurnStepSplitIndex(withNew, 1, TurnAnchor{}); !ok || idx != len(withNew)-2 {
		t.Fatalf("idx = %d ok = %v, want %d", idx, ok, len(withNew)-2)
	}
	if _, ok := TurnStepSplitIndex(withNew, 2, TurnAnchor{}); ok {
		t.Fatal("the steps before the new prompt were counted as steps of its turn")
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
		{name: "the summary row stands for the prompt", msgs: concatMessages([]llm.Message{NewInTurnCompactionSummaryMessage(TurnSourceUser, "p", "s", "m")}, stepOf("a"), stepOf("b")), want: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TurnStepCount(tc.msgs, TurnAnchor{}); got != tc.want {
				t.Fatalf("TurnStepCount = %d, want %d", got, tc.want)
			}
			// The two functions agree: the split index exists exactly for the
			// kept counts below the number of steps.
			for keep := 0; keep <= tc.want+2; keep++ {
				_, ok := TurnStepSplitIndex(tc.msgs, keep, TurnAnchor{})
				if ok != (keep < tc.want) {
					t.Fatalf("keep %d: ok = %v with %d steps", keep, ok, tc.want)
				}
			}
		})
	}
}

func TestTurnPrompt(t *testing.T) {
	prompt := "fix the failing build\n\nand explain why"
	t.Run("the last real user message of the transcript", func(t *testing.T) {
		msgs := concatMessages([]llm.Message{userMsg("old"), assistantMsg("ok"), userMsg(prompt)}, stepOf("c1"))
		got, ok := TurnPrompt(msgs, TurnAnchor{})
		if !ok || got != prompt {
			t.Fatalf("got %q ok = %v, want the prompt verbatim", got, ok)
		}
	})

	t.Run("a prompt hidden behind a summary row is still found", func(t *testing.T) {
		msgs := concatMessages(
			[]llm.Message{userMsg(prompt)}, stepOf("c1"),
			[]llm.Message{NewInTurnCompactionSummaryMessage(TurnSourceUser, prompt, "s", "m")}, stepOf("c2"),
		)
		got, ok := TurnPrompt(msgs, TurnAnchor{})
		if !ok || got != prompt {
			t.Fatalf("got %q ok = %v", got, ok)
		}
	})

	t.Run("a summary row is never the prompt", func(t *testing.T) {
		msgs := []llm.Message{NewCompactionSummaryMessage("only a summary", "m"), assistantMsg("a")}
		if got, ok := TurnPrompt(msgs, TurnAnchor{}); ok {
			t.Fatalf("got %q for a history with no user message", got)
		}
	})

	t.Run("a blank prompt is no prompt", func(t *testing.T) {
		if got, ok := TurnPrompt([]llm.Message{userMsg("earlier"), assistantMsg("a"), userMsg("  \n")}, TurnAnchor{}); ok {
			t.Fatalf("got %q: the latest prompt is blank, an older one must not stand in for it", got)
		}
		if _, ok := TurnPrompt(nil, TurnAnchor{}); ok {
			t.Fatal("an empty history has no prompt")
		}
	})
}

func TestNewInTurnCompactionSummaryMessage(t *testing.T) {
	m := NewInTurnCompactionSummaryMessage(TurnSourceUser, "fix the build", "  steps one to four  ", "prov/model")
	if m.Role != llm.RoleUser || !m.CompactionSummary || m.Model != "prov/model" || m.CreatedAt == "" {
		t.Fatalf("not shaped like a summary row: %+v", m)
	}
	const want = "fix the build\n\n" +
		"The text above is the user's request, verbatim, followed by any follow-up messages the user sent while it was being worked on. " +
		"Coddy compacted the conversation before this point, " +
		"including the earlier steps of the work on this request. Summary of the compacted part:\n\n" +
		"steps one to four"
	if m.Content != want {
		t.Fatalf("content layout changed:\n got: %q\nwant: %q", m.Content, want)
	}
	if !strings.HasPrefix(m.Content, "fix the build") {
		t.Fatal("the row must begin with the prompt")
	}
}

// After an in-turn fold the window opens with a summary row and the steps that
// were kept. Without counting that row as the start of a turn, a new prompt
// would find "one user turn" in the window and the old row and its steps could
// never be folded.
func TestCompactionSplitIndexTreatsAnInTurnSummaryAsATurn(t *testing.T) {
	inTurn := NewInTurnCompactionSummaryMessage(TurnSourceUser, "do it", "earlier steps", "m")
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
