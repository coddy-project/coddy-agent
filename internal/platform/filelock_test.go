//go:build unix || windows

package platform

import (
	"path/filepath"
	"testing"
	"time"
)

// A second holder waits while the first holds the lock and gets it once the
// first lets go. Two opens of one path in one process are two holders to both
// flock and LockFileEx, so the test stands in for two processes.
func TestLockFileHoldsOffASecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json.lock")
	release, err := LockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan func(), 1)
	go func() {
		second, err := LockFile(path)
		if err != nil {
			t.Error(err)
			second = func() {}
		}
		acquired <- second
	}()
	select {
	case second := <-acquired:
		second()
		release()
		t.Fatal("a second holder took the lock while the first held it")
	case <-time.After(200 * time.Millisecond):
	}
	release()
	select {
	case second := <-acquired:
		second()
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was not handed on after its release")
	}
}
