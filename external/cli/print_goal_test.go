//go:build cli

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// In a one-shot run every goal turn's answer starts a paragraph of its own on
// stdout, and the reason the supervisor started it goes to stderr.
func TestPrintModeSeparatesGoalTurns(t *testing.T) {
	var out, errOut bytes.Buffer
	p := &printSender{out: &out, errOut: &errOut}
	chunk := func(text string) acp.MessageChunkUpdate {
		return acp.MessageChunkUpdate{SessionUpdate: "agent_message_chunk", Content: acp.ContentBlock{Type: "text", Text: text}}
	}
	_ = p.SendSessionUpdate("s", acp.GoalTurnUpdate{SessionUpdate: acp.UpdateTypeGoalTurn, Kind: acp.GoalTurnKickoff, Objective: "fix it"})
	_ = p.SendSessionUpdate("s", chunk("First answer."))
	_ = p.SendSessionUpdate("s", acp.GoalTurnUpdate{SessionUpdate: acp.UpdateTypeGoalTurn, Kind: acp.GoalTurnContinue, Index: 1, Limit: 10, Objective: "fix it", Reason: "tests fail"})
	_ = p.SendSessionUpdate("s", chunk("Second answer."))
	_ = p.SendSessionUpdate("s", acp.SessionGoalUpdate{SessionUpdate: acp.UpdateTypeSessionGoal, Notice: "Goal complete: fix it"})
	if out.String() != "First answer.\n\nSecond answer." {
		t.Fatalf("stdout = %q", out.String())
	}
	for _, want := range []string{"[goal] Goal set: fix it", "[goal] Goal continuation 1 of 10: tests fail", "[goal] Goal complete: fix it"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut.String())
		}
	}
}
