package session

import (
	"testing"
	"time"
)

func TestMarkTurnActiveNestedReleaseKeepsOuterTurn(t *testing.T) {
	m := &Manager{}
	if m.SessionTurnActiveInProcess("sess_nested") {
		t.Fatal("no turn was marked yet")
	}

	// HandleSessionPromptWithSender delegates to RunPlan, so one logical turn can be
	// marked twice. The inner release must not report the session as idle.
	outer := m.markTurnActive("sess_nested")
	inner := m.markTurnActive("sess_nested")

	inner()
	if !m.SessionTurnActiveInProcess("sess_nested") {
		t.Fatal("inner release cleared the outer turn")
	}

	outer()
	if m.SessionTurnActiveInProcess("sess_nested") {
		t.Fatal("session still active after the outer release")
	}
}

func TestMarkTurnActiveReleaseIsIdempotent(t *testing.T) {
	m := &Manager{}
	outer := m.markTurnActive("sess_twice")
	inner := m.markTurnActive("sess_twice")

	// A release closure can be invoked more than once (a deferred call plus an
	// explicit one); repeats must not underflow the refcount.
	outer()
	outer()
	if !m.SessionTurnActiveInProcess("sess_twice") {
		t.Fatal("repeated release consumed the inner turn")
	}

	inner()
	if m.SessionTurnActiveInProcess("sess_twice") {
		t.Fatal("session still active after every release")
	}
}

func TestMarkTurnActiveIgnoresBlankSessionID(t *testing.T) {
	m := &Manager{}
	release := m.markTurnActive("   ")
	if m.SessionTurnActiveInProcess("") {
		t.Fatal("a blank session id must never register a turn")
	}
	release()
}

func TestSessionTurnActiveInProcessTrimsSessionID(t *testing.T) {
	m := &Manager{}
	release := m.markTurnActive(" sess_pad ")
	defer release()
	if !m.SessionTurnActiveInProcess("sess_pad") {
		t.Fatal("session id should be compared trimmed")
	}
}

func TestTurnStartedAtIsKeptForTheWholeTurnAndMatchesTheEvent(t *testing.T) {
	m := &Manager{}
	var started TurnEvent
	remove := m.AddTurnObserver(func(ev TurnEvent) {
		if ev.Phase == TurnPhaseStarted {
			started = ev
		}
	})
	defer remove()

	if _, ok := m.TurnStartedAt("sess_clock"); ok {
		t.Fatal("an idle session has no turn start")
	}
	outer := m.markTurnActive("sess_clock")
	at, ok := m.TurnStartedAt("sess_clock")
	if !ok || at.IsZero() {
		t.Fatalf("running turn start = %v, %v", at, ok)
	}
	if !started.At.Equal(at) {
		t.Fatalf("turn_started carries %v, the registry %v", started.At, at)
	}

	// The inner admission of the same turn (RunPlan) must not restart the clock.
	inner := m.markTurnActive("sess_clock")
	if again, _ := m.TurnStartedAt("sess_clock"); !again.Equal(at) {
		t.Fatalf("nested admission moved the start from %v to %v", at, again)
	}
	inner()
	if again, ok := m.TurnStartedAt("sess_clock"); !ok || !again.Equal(at) {
		t.Fatal("inner release dropped the start of the outer turn")
	}
	outer()
	if _, ok := m.TurnStartedAt("sess_clock"); ok {
		t.Fatal("the start outlived the turn")
	}
}

func TestOldestTurnStartedAt(t *testing.T) {
	m := &Manager{}
	if _, ok := m.OldestTurnStartedAt(); ok {
		t.Fatal("an idle manager has no oldest turn")
	}

	older := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	releaseOld := m.markTurnActive("sess_old")
	releaseNew := m.markTurnActive("sess_new")
	m.activeTurnMu.Lock()
	m.turnStarted["sess_old"], m.turnStarted["sess_new"] = older, older.Add(time.Minute)
	m.activeTurnMu.Unlock()

	if at, ok := m.OldestTurnStartedAt(); !ok || !at.Equal(older) {
		t.Fatalf("oldest turn = %v, %v; want %v", at, ok, older)
	}
	releaseOld()
	if at, ok := m.OldestTurnStartedAt(); !ok || !at.Equal(older.Add(time.Minute)) {
		t.Fatalf("after the oldest ended: %v, %v; want the other turn's start", at, ok)
	}
	releaseNew()
	if _, ok := m.OldestTurnStartedAt(); ok {
		t.Fatal("no turn runs, yet one has a start")
	}
}
