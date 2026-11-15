package serve

import (
	"context"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// senderProbe is an acp.UpdateSender that only records how many updates it
// got; what a mirror's wrapper does with it is the mirror's business.
type senderProbe struct{ updates int }

func (s *senderProbe) SendSessionUpdate(string, interface{}) error {
	s.updates++
	return nil
}
func (s *senderProbe) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return nil, nil
}
func (s *senderProbe) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return nil, nil
}

// More than one surface can mirror a turn - the desktop's window server and
// its public HTTP server share the sessions, so both have to see them. The
// mirrors are a set, not a slot: each one registered gets the wrap, and each
// one's stop ends only its own mirroring.
func TestEveryRegisteredMirrorSeesTheTurn(t *testing.T) {
	rt := &Runtime{}
	first, second := &recordingMirror{}, &recordingMirror{}
	withdrawFirst := rt.AddTurnMirror(first)
	withdrawSecond := rt.AddTurnMirror(second)

	sender, stop := rt.MirrorTurn("sess_1", &senderProbe{})
	if err := sender.SendSessionUpdate("sess_1", nil); err != nil {
		t.Fatal(err)
	}
	if len(first.seen()) != 1 || len(second.seen()) != 1 {
		t.Fatalf("first saw %v, second saw %v", first.seen(), second.seen())
	}

	// Withdrawing one surface's mirror leaves the other in place.
	withdrawFirst()
	_, stop2 := rt.MirrorTurn("sess_2", &senderProbe{})
	if len(first.seen()) != 1 {
		t.Fatalf("the withdrawn mirror still saw %v", first.seen())
	}
	if len(second.seen()) != 2 {
		t.Fatalf("the live mirror saw %v", second.seen())
	}

	withdrawSecond()
	stop()
	stop2()

	// With nothing registered the primary sender comes back untouched and the
	// stop is a no-op.
	primary := &senderProbe{}
	got, stop3 := rt.MirrorTurn("sess_3", primary)
	same, ok := got.(*senderProbe)
	if !ok || same != primary {
		t.Fatal("an empty mirror set did not hand the primary sender back")
	}
	if err := got.SendSessionUpdate("sess_3", nil); err != nil {
		t.Fatal(err)
	}
	stop3()
	if primary.updates != 1 {
		t.Fatalf("the primary sender got %d updates", primary.updates)
	}
}

// SetTurnMirror keeps its single-surface semantics for the callers that have
// one surface: it replaces the whole set, and nil clears it.
func TestSetTurnMirrorReplacesTheWholeSet(t *testing.T) {
	rt := &Runtime{}
	old := &recordingMirror{}
	rt.AddTurnMirror(old)
	fresh := &recordingMirror{}
	rt.SetTurnMirror(fresh)

	if _, stop := rt.MirrorTurn("sess_1", &senderProbe{}); stop == nil {
		t.Fatal("no stop")
	}
	if len(old.seen()) != 0 || len(fresh.seen()) != 1 {
		t.Fatalf("old saw %v, fresh saw %v", old.seen(), fresh.seen())
	}

	rt.SetTurnMirror(nil)
	primary := &senderProbe{}
	got, stop := rt.MirrorTurn("sess_2", primary)
	same, ok := got.(*senderProbe)
	if !ok || same != primary {
		t.Fatal("a cleared set did not hand the primary sender back")
	}
	stop()
}
