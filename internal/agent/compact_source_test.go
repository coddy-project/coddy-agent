package agent

// The summary row of a fold inside a turn says whose text it carries in front of
// the summary (issue #490): the opening message of a turn is not always the
// user's. A goal turn the supervisor started opens with its own instruction and a
// turn woken by a finished background task with the notice, and a row that called
// either "the user's request" would tell the model the user asked for it.

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

var openingSources = []struct {
	name    string
	opening llm.Message
	source  session.TurnSource
	says    string
}{
	{"a prompt the user typed", stamped("Audit every module.", openedAt), session.TurnSourceUser,
		"The text above is the user's request, verbatim"},
	{"a goal turn the supervisor started", llm.Message{Role: llm.RoleUser, CreatedAt: openedAt,
		Content:  "SUPERVISOR: keep working toward the goal: audit every module.",
		GoalTurn: &llm.GoalTurn{Kind: "continue", Index: 2, Limit: 10, Objective: "audit every module"}}, session.TurnSourceSupervisor,
		"The text above is the instruction the session's goal supervisor started this turn with, verbatim"},
	{"a turn woken by a finished background task", llm.Message{Role: llm.RoleUser, CreatedAt: openedAt,
		Content:        "Background task bg_1 finished: make test failed with exit 2.",
		BackgroundWake: &llm.BackgroundWake{}}, session.TurnSourceBackground,
		"The text above is the notice that woke this turn when a background task finished, verbatim"},
}

func TestInTurnFoldNamesWhoOpenedTheTurn(t *testing.T) {
	for _, tc := range openingSources {
		t.Run(tc.name, func(t *testing.T) {
			st := stateOf(t, []llm.Message{tc.opening}, steps("s", 6))
			two := 2
			provider := &compactCannedProvider{t: t, summary: "the first four steps read the modules"}
			ag := inTurnAgent(t, st, config.Compaction{InTurn: config.InTurn{KeepRecentSteps: &two}}, provider)

			res, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true})
			if err != nil {
				t.Fatal(err)
			}
			if !res.InTurn || res.KeptSteps != 2 {
				t.Fatalf("result = %+v, want an in-turn fold keeping two steps", res)
			}
			row := windowOf(st)[0]
			if !row.CompactionSummary || !strings.HasPrefix(row.Content, tc.opening.Content+"\n\n"+tc.says+", followed by any follow-up messages") {
				t.Fatalf("the row must carry the opening message and then name its writer (%q), got %q", tc.says, firstChars(row.Content, 300))
			}
			if _, _, source, ok := session.SplitInTurnRow(row.Content); !ok || source != tc.source {
				t.Fatalf("the row reads back as source %v (ok = %v), want %v", source, ok, tc.source)
			}
			if !strings.HasSuffix(row.Content, "Summary of the compacted part:\n\nthe first four steps read the modules") {
				t.Fatalf("the row must end in the common tail and the summary: %q", row.Content)
			}
		})
	}
}

// A compact_context call made inside such a turn writes the same row.
func TestCompactContextNamesWhoOpenedTheTurn(t *testing.T) {
	for _, tc := range openingSources[1:] {
		t.Run(tc.name, func(t *testing.T) {
			st := stateOf(t, []llm.Message{tc.opening}, steps("s", 3), []llm.Message{compactContextCall()})
			provider := &compactCannedProvider{t: t, summary: "the first three reads"}
			ag := inTurnAgent(t, st, config.Compaction{}, provider)

			res, err := ag.CompactSession(context.Background(), CompactOptions{Force: true, FromTool: true})
			if err != nil {
				t.Fatal(err)
			}
			if !res.InTurn {
				t.Fatalf("result = %+v, want a fold that kept the opening message in front", res)
			}
			row := windowOf(st)[0]
			if !strings.HasPrefix(row.Content, tc.opening.Content+"\n\n"+tc.says) {
				t.Fatalf("the row must name the writer of the opening message, got %q", firstChars(row.Content, 300))
			}
		})
	}
}

// A second fold folds the first row, which carries the opening message, and
// writes a row of the same source: the source is read off the opening message in
// the transcript, not off the row it replaces.
func TestSecondInTurnFoldKeepsTheSource(t *testing.T) {
	opening := openingSources[1].opening
	st := stateOf(t, []llm.Message{opening}, steps("a", 6))
	two := 2
	provider := &compactCannedProvider{t: t, summary: "earlier work"}
	ag := inTurnAgent(t, st, config.Compaction{InTurn: config.InTurn{KeepRecentSteps: &two}}, provider)
	if _, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true}); err != nil {
		t.Fatal(err)
	}
	for _, m := range steps("b", 4) {
		st.AddMessage(m)
	}
	if _, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true}); err != nil {
		t.Fatal(err)
	}
	row := windowOf(st)[0]
	if _, _, source, ok := session.SplitInTurnRow(row.Content); !ok || source != session.TurnSourceSupervisor {
		t.Fatalf("the second row reads back as source %v (ok = %v), want the supervisor", source, ok)
	}
	if got := strings.Count(row.Content, opening.Content); got != 1 {
		t.Fatalf("the second row holds the instruction %d times, want once", got)
	}
}
