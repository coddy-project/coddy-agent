package session

import "testing"

func TestAcquireStubTurnLockReturnsBusy(t *testing.T) {
	mgr := &Manager{}
	unlock, err := mgr.acquireStubTurnLock("session")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	if _, err := mgr.acquireStubTurnLock("session"); err != ErrSessionTurnBusy {
		t.Fatalf("second lock error = %v, want %v", err, ErrSessionTurnBusy)
	}
}
