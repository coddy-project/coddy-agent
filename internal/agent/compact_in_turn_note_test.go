package agent

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// The summarizer of a fold inside a turn is told the work is still going on and
// that the message the turn answers stays in front of its summary. It is told
// who wrote that message: in a goal turn or a woken turn it is not the user's
// request, and a summary that calls it so misleads the next request the same
// way the row's preamble used to.
func TestInTurnSummaryInstructionsNameWhoStartedTheTurn(t *testing.T) {
	cases := []struct {
		name   string
		source session.TurnSource
		want   string
	}{
		{"user", session.TurnSourceUser, "on the user's latest request"},
		{"supervisor", session.TurnSourceSupervisor, "on the instruction the session's goal supervisor started this turn with"},
		{"background", session.TurnSourceBackground, "on the notice that woke this turn when a background task finished"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := withInTurnSummaryInstructions("", c.source)
			if !strings.Contains(got, c.want) {
				t.Fatalf("note %q does not say %q", got, c.want)
			}
			if c.source != session.TurnSourceUser && strings.Contains(got, "user's") {
				t.Fatalf("note for a %s turn calls the message the user's: %q", c.name, got)
			}
			if !strings.Contains(got, "stays verbatim in front of your summary") {
				t.Fatalf("note %q no longer says the message stays in front of the summary", got)
			}
		})
	}

	got := withInTurnSummaryInstructions("keep the file paths", session.TurnSourceSupervisor)
	if !strings.HasPrefix(got, "keep the file paths\n\n") {
		t.Fatalf("the operator's instructions must come first: %q", got)
	}
}
