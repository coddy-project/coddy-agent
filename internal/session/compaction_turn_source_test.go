package session

// Who wrote the message a turn opened with, and what the summary row of an
// in-turn fold says about it (issue #490): the request of the user, the
// instruction the session's goal supervisor started the turn with, or the notice
// that woke the turn when a background task finished.

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// goalStartIndex for the implicit goal of supervisor.enable is the latest
// request. A fold inside the turn inserts a user-role summary row after the
// prompt, and that row is no request.
func TestGoalStartIndexSkipsASummaryRow(t *testing.T) {
	prompt := llm.Message{Role: llm.RoleUser, Content: "Fix the parser."}
	row := NewInTurnCompactionSummaryMessage(TurnSourceUser, "Fix the parser.", "the worker read parse.go", "m")
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

// sources are the three kinds of message a turn opens with, each with a message
// of that kind and the words its row uses to say whose text it carries.
var turnSources = []struct {
	name    string
	source  TurnSource
	opening llm.Message
	says    string
}{
	{"the user", TurnSourceUser, llm.Message{Role: llm.RoleUser, Content: "Fix the parser."}, "The text above is the user's request, verbatim"},
	{"the goal supervisor", TurnSourceSupervisor, llm.Message{Role: llm.RoleUser, Content: "SUPERVISOR: keep working toward the goal: fix the parser.",
		GoalTurn: &llm.GoalTurn{Kind: "continue", Index: 2, Limit: 10, Objective: "fix the parser"}},
		"The text above is the instruction the session's goal supervisor started this turn with, verbatim"},
	{"a finished background task", TurnSourceBackground, llm.Message{Role: llm.RoleUser, Content: "Background task bg_1 finished: make test failed.",
		BackgroundWake: &llm.BackgroundWake{}},
		"The text above is the notice that woke this turn when a background task finished, verbatim"},
}

func TestTurnSourceOf(t *testing.T) {
	for _, tc := range turnSources {
		if got := TurnSourceOf(tc.opening); got != tc.source {
			t.Errorf("%s: TurnSourceOf = %v, want %v", tc.name, got, tc.source)
		}
	}
	t.Run("a plain prompt with attachments is the user's", func(t *testing.T) {
		if got := TurnSourceOf(llm.Message{Role: llm.RoleUser, Content: "see this", ImageParts: []llm.ImagePart{{}}}); got != TurnSourceUser {
			t.Fatalf("TurnSourceOf = %v", got)
		}
	})
}

// The row says whose text is in front of the summary, and every variant ends
// in the same words, which the web UI cuts the body at.
func TestInTurnRowNamesWhoWroteTheOpeningMessage(t *testing.T) {
	const tail = "Coddy compacted the conversation before this point, including the earlier steps of the work on this request. Summary of the compacted part:\n\n"
	for _, tc := range turnSources {
		t.Run(tc.name, func(t *testing.T) {
			row := NewInTurnCompactionSummaryMessage(tc.source, tc.opening.Content, "the worker read two files", "m")
			if !row.CompactionSummary || !strings.HasPrefix(row.Content, tc.opening.Content+"\n\n"+tc.says+", followed by any follow-up messages the user sent while it was being worked on. "+tail) {
				t.Fatalf("row = %q", row.Content)
			}
			if !strings.HasSuffix(row.Content, tail+"the worker read two files") {
				t.Fatalf("the row does not end in the common tail and the summary: %q", row.Content)
			}
			// What the web UI renders is what follows the last words of the preamble.
			if _, body, ok := strings.Cut(row.Content, "Summary of the compacted part:"); !ok || strings.TrimSpace(body) != "the worker read two files" {
				t.Fatalf("the body after the UI's cut is %q", body)
			}
		})
	}
}

func TestSplitInTurnRowReadsEveryVariant(t *testing.T) {
	prefix := "Keep the API stable.\n\nFollow-up the user sent while this request was being worked on:\nkeep the tests green"
	for _, tc := range turnSources {
		t.Run(tc.name, func(t *testing.T) {
			row := NewInTurnCompactionSummaryMessage(tc.source, prefix, "  the worker read two files  ", "m")

			prompt, summary, src, ok := SplitInTurnRow(row.Content)
			if !ok || prompt != prefix || summary != "the worker read two files" || src != tc.source {
				t.Fatalf("SplitInTurnRow = %q / %q / %v / %v", prompt, summary, src, ok)
			}
			// The splitter the other readers use finds the same boundary.
			if prompt, summary, ok := SplitInTurnSummary(row.Content); !ok || prompt != prefix || summary != "the worker read two files" {
				t.Fatalf("SplitInTurnSummary = %q / %q / %v", prompt, summary, ok)
			}
			if got := SummaryBody(row.Content); got != "the worker read two files" {
				t.Fatalf("SummaryBody = %q", got)
			}
			// A replay shows the row from its preamble on, whatever the source: the
			// request was replayed at its own place.
			replay := SummaryReplayText(row.Content)
			if !strings.HasPrefix(replay, tc.says) || !strings.HasSuffix(replay, "Summary of the compacted part:\n\nthe worker read two files") || strings.Contains(replay, "Keep the API stable") {
				t.Fatalf("SummaryReplayText = %q", replay)
			}
		})
	}

	t.Run("the earliest preamble of any variant is the boundary", func(t *testing.T) {
		user := NewInTurnCompactionSummaryMessage(TurnSourceUser, "", "", "m").Content
		sup := NewInTurnCompactionSummaryMessage(TurnSourceSupervisor, "", "", "m").Content
		// A summary that quotes another variant keeps the quote.
		row := NewInTurnCompactionSummaryMessage(TurnSourceSupervisor, "the instruction", "it said:"+user, "m")
		prompt, summary, src, ok := SplitInTurnRow(row.Content)
		if !ok || prompt != "the instruction" || src != TurnSourceSupervisor || !strings.HasPrefix(summary, "it said:") || !strings.Contains(summary, "The text above is the user's request") {
			t.Fatalf("got %q / %q / %v / %v", prompt, summary, src, ok)
		}
		// And the other way round.
		row = NewInTurnCompactionSummaryMessage(TurnSourceUser, "the request", "it said:"+sup, "m")
		prompt, summary, src, ok = SplitInTurnRow(row.Content)
		if !ok || prompt != "the request" || src != TurnSourceUser || !strings.Contains(summary, "goal supervisor") {
			t.Fatalf("got %q / %q / %v / %v", prompt, summary, src, ok)
		}
	})

	t.Run("a plain row and ordinary text are not rows", func(t *testing.T) {
		quoting := NewCompactionSummaryMessage("the row said:\n\n"+NewInTurnCompactionSummaryMessage(TurnSourceBackground, "p", "s", "m").Content, "m")
		for _, content := range []string{quoting.Content, "", "a prompt\n\nThe text above is the notice that woke this turn"} {
			if _, _, _, ok := SplitInTurnRow(content); ok {
				t.Fatalf("%q was taken for an in-turn row", content)
			}
		}
	})
}

// The digest labels what is in front of the summary with the writer of the
// opening message: a supervisor's instruction is not the operator's text.
func TestGoalDigestLabelsTheRowByWhoStartedTheTurn(t *testing.T) {
	for _, tc := range turnSources {
		t.Run(tc.name, func(t *testing.T) {
			row := NewInTurnCompactionSummaryMessage(tc.source, tc.opening.Content, "the worker changed two functions", "m")
			d := digestOf(row)
			label := map[TurnSource]string{TurnSourceUser: "[operator] ", TurnSourceSupervisor: "[supervisor] ", TurnSourceBackground: "[background] "}[tc.source]
			if !strings.Contains(d, label+tc.opening.Content+"\n") {
				t.Errorf("digest lacks %q:\n%s", label+tc.opening.Content, d)
			}
			for other, otherLabel := range map[TurnSource]string{TurnSourceUser: "[operator] ", TurnSourceSupervisor: "[supervisor] ", TurnSourceBackground: "[background] "} {
				if other != tc.source && strings.Contains(d, otherLabel+tc.opening.Content) {
					t.Errorf("the prefix is also labelled %q:\n%s", otherLabel, d)
				}
			}
			if !strings.Contains(d, "[summary of earlier steps of this request, written by the worker: claims, not evidence] the worker changed two functions") {
				t.Errorf("the summary part lost its label:\n%s", d)
			}
			if strings.Contains(d, "The text above is") {
				t.Errorf("the preamble leaked into the digest:\n%s", d)
			}
		})
	}
}
