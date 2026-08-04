package session

import "testing"

func TestAcquireStubTurnLockReturnsBusy(t *testing.T) {
	mgr := &Manager{}
	unlock, err := mgr.acquireStubTurnLock("sess_busy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.acquireStubTurnLock("sess_busy"); err != ErrSessionTurnBusy {
		t.Fatalf("second lock error = %v, want %v", err, ErrSessionTurnBusy)
	}
	unlock()

	unlockAgain, err := mgr.acquireStubTurnLock("sess_busy")
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	unlockAgain()
}
