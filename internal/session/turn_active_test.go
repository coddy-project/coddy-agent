package session

import "testing"

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
