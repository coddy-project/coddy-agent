package session_test

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

var errDeleteStop = errors.New("stop failed")

type failingDeleteHandle struct{ *holdHandle }

func (h *failingDeleteHandle) Stop(time.Duration) error { return errDeleteStop }

type unsettledDeleteHandle struct{ *holdHandle }

func (h *unsettledDeleteHandle) Stop(time.Duration) error { return nil }

func TestDeleteSessionTreeReportsUnsettledTaskAsConflict(t *testing.T) {
	restore := session.SetDeleteSettleTimeoutForTest(30 * time.Millisecond)
	defer restore()
	m, store, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})
	h := &unsettledDeleteHandle{newHoldHandle()}
	t.Cleanup(func() { _ = h.holdHandle.Stop(0) })
	_, err := pool.Launch(bgtask.Spec{SessionID: parent.ID, Kind: bgtask.KindCommand, Command: "fixture"}, func(string, io.Writer) (bgtask.Handle, error) { return h, nil })
	if err != nil {
		t.Fatal(err)
	}
	err = m.DeleteSessionTree(parent.ID, pool)
	var conflict *session.SessionDeleteConflict
	if !errors.Is(err, bgtask.ErrStopNotSettled) || !errors.As(err, &conflict) || conflict.SessionID != parent.ID {
		t.Fatalf("conflict = %v", err)
	}
	if !store.HasPersistedSnapshot(parent.ID) {
		t.Fatal("bundle removed after stop timed out")
	}
}

func TestDeleteSessionTreePreservesBundleWhenTaskStopFails(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})
	h := &failingDeleteHandle{newHoldHandle()}
	t.Cleanup(func() { _ = h.holdHandle.Stop(0) })
	_, err := pool.Launch(bgtask.Spec{SessionID: parent.ID, Kind: bgtask.KindCommand, Command: "fixture"}, func(string, io.Writer) (bgtask.Handle, error) { return h, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteSessionTree(parent.ID, pool); !errors.Is(err, errDeleteStop) {
		t.Fatalf("delete error = %v, want stop error", err)
	}
	if !store.HasPersistedSnapshot(parent.ID) {
		t.Fatal("bundle removed after task stop failed")
	}
	result := make(chan error, 1)
	go func() { result <- m.DeleteSessionTree(parent.ID, pool) }()
	select {
	case err := <-result:
		if !errors.Is(err, errDeleteStop) {
			t.Fatalf("retry error = %v, want stop error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry blocked after failed stop")
	}
	if !store.HasPersistedSnapshot(parent.ID) {
		t.Fatal("retry removed unsettled bundle")
	}
}
