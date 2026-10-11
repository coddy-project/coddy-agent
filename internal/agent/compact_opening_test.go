package agent

// Where the turn in progress opened is recorded on the session by index
// (issue #490), so a follow-up that repeats the prompt word for word within the
// same second is not taken for it, and a turn resumed after a permission answer
// - a new Agent over the same session - finds the record too.

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// The prompt and a queued follow-up with the same words, written in the same
// second: the record by text and time matched the later one, and the fold saw a
// turn with two steps instead of five.
func TestInTurnFoldFindsThePromptWhenAFollowUpRepeatsIt(t *testing.T) {
	msgs := concatMsgs(
		[]llm.Message{stamped("continue", openedAt)}, steps("a", 3),
		[]llm.Message{stamped("continue", openedAt)}, steps("b", 2),
	)
	st := stateOpenedAt(t, 0, msgs)
	two := 2
	provider := &compactCannedProvider{t: t, summary: "the first three steps"}
	ag := inTurnAgent(t, st, config.Compaction{InTurn: config.InTurn{KeepRecentSteps: &two}}, provider)

	res, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true})
	if err != nil {
		t.Fatalf("the turn has five steps and the fold keeps two: %v", err)
	}
	if !res.InTurn || res.KeptSteps != 2 {
		t.Fatalf("result = %+v, want an in-turn fold keeping two steps", res)
	}
	row := windowOf(st)[0]
	want := "continue\n\n" + inTurnFollowUpIntro + "\ncontinue\n\nThe text above is"
	if !strings.HasPrefix(row.Content, want) {
		t.Fatalf("the row must carry the prompt and then the repeated follow-up, got %q", firstChars(row.Content, 200))
	}
}

// A turn resumed after a permission answer runs on a new Agent over the session
// its first part ran on, and the session still knows where the turn opened.
func TestAFreshAgentOverTheSameSessionFindsTheTurnOpening(t *testing.T) {
	msgs := followUpTurn()
	st := stateOpenedAt(t, 0, msgs)
	two := 2
	// Not the Agent that ran the first part of the turn: nothing on it records
	// the opening.
	provider := &compactCannedProvider{t: t, summary: "audited the first modules"}
	ag := inTurnAgent(t, st, config.Compaction{InTurn: config.InTurn{KeepRecentSteps: &two}}, provider)

	res, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.InTurn {
		t.Fatalf("result = %+v, want an in-turn fold", res)
	}
	row := windowOf(st)[0]
	want := followUpPrompt + "\n\n" + inTurnFollowUpIntro + "\nAlso keep the tests green.\n\nThe text above is"
	if !strings.HasPrefix(row.Content, want) {
		t.Fatalf("the row must begin with the prompt the session recorded, not the last follow-up: %q", firstChars(row.Content, 200))
	}
}

// With no record - the process restarted since the turn began - the fallback is
// the last user message that is not a Stop-hook follow-up, as before: the known
// limit that the record exists to lift.
func TestWithoutARecordTheFallbackStillApplies(t *testing.T) {
	st := stateOf(t, followUpTurn())
	ag := inTurnAgent(t, st, config.Compaction{}, &compactCannedProvider{t: t, summary: "unused"})
	if got := st.TurnAnchor(); got != (session.TurnAnchor{}) {
		t.Fatalf("fixture: the session has a record: %+v", got)
	}
	msgs := st.GetMessages()
	prefix, _, ok := ag.inTurnPrefix(msgs, len(msgs), 128000)
	if !ok || !strings.HasPrefix(prefix, "And leave the docs alone.") {
		t.Fatalf("prefix = %q ok = %v, want the last queued follow-up standing in for the prompt", firstChars(prefix, 80), ok)
	}
}
