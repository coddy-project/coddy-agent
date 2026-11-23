//go:build unix

package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPromptLockDoesNotUnlinkHeldMalformedLock(t *testing.T) {
	dir := t.TempDir()
	unlock, err := acquireTurnLockFlock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	path := filepath.Join(dir, turnLockFileName)
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{}
	_, err = m.acquirePromptTurnLock("test", &State{SessionDir: dir})
	if !errors.Is(err, ErrSessionTurnBusy) {
		t.Fatalf("lock error = %v", err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("held lock replaced: %v", err)
	}
}
