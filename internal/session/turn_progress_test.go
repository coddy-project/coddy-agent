package session

import (
	"testing"
	"time"
)

func TestTurnProgressSurvivesANestedAdmissionOfTheSameTurn(t *testing.T) {
	st := &State{ID: "sess_progress"}
	start := time.Now().UTC()
	st.BeginTurnProgress(start)
	st.SetTurnOutputTokens(120, true)

	// RunPlan admits the same turn again with the same start.
	st.BeginTurnProgress(start)
	got, ok := st.TurnProgress()
	if !ok || got.OutputTokens != 120 || !got.Estimated || !got.StartedAt.Equal(start) {
		t.Fatalf("nested admission reset the progress: %+v, %v", got, ok)
	}
}

func TestTurnProgressOfANewTurnStartsFromZero(t *testing.T) {
	st := &State{ID: "sess_progress"}
	first := time.Now().UTC()
	st.BeginTurnProgress(first)
	st.SetTurnOutputTokens(500, false)

	second := first.Add(time.Minute)
	st.BeginTurnProgress(second)
	got, _ := st.TurnProgress()
	if got.OutputTokens != 0 || !got.StartedAt.Equal(second) {
		t.Fatalf("new turn progress = %+v, want zero tokens from %v", got, second)
	}
}

func TestEndTurnProgressLeavesANewerTurnAlone(t *testing.T) {
	st := &State{ID: "sess_progress"}
	first := time.Now().UTC()
	second := first.Add(time.Second)
	st.BeginTurnProgress(first)
	// The next turn was admitted while the first one was still releasing.
	st.BeginTurnProgress(second)
	st.EndTurnProgress(first)
	if got, ok := st.TurnProgress(); !ok || !got.StartedAt.Equal(second) {
		t.Fatalf("the late release of the first turn closed the second: %+v, %v", got, ok)
	}
	st.EndTurnProgress(second)
	if _, ok := st.TurnProgress(); ok {
		t.Fatal("progress outlived its turn")
	}
}

func TestSetTurnOutputTokensOutsideATurnIsDropped(t *testing.T) {
	st := &State{ID: "sess_progress"}
	st.SetTurnOutputTokens(42, false)
	if got, ok := st.TurnProgress(); ok || got.OutputTokens != 0 {
		t.Fatalf("idle session recorded progress: %+v, %v", got, ok)
	}
}
