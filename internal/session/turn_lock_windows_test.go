package session

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsTurnLockCrossProcess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Bundle")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := new(Manager)
	st := &State{ID: "test", SessionDir: dir}
	assertWindowsTurnLockChild(t, dir, "idle")
	unlock, err := m.acquirePromptTurnLock(st.ID, st)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unlock)
	assertWindowsTurnLockChild(t, filepath.Join(filepath.Dir(dir), strings.ToUpper(filepath.Base(dir))), "busy")

	// The unlock callback need not run on the acquiring goroutine.
	done := make(chan struct{})
	go func() {
		unlock()
		close(done)
	}()
	<-done
	assertWindowsTurnLockChild(t, dir, "idle")

	deleteUnlock, err := m.acquireDeleteTurnLock(st.ID, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer deleteUnlock()
	assertWindowsTurnLockChild(t, dir, "busy")
	// Exercise the same removal as DeleteSessionTree, while still holding its
	// lock; Windows refuses removal if a non-delete-sharing handle is open.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove while holding delete lock: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("bundle should be gone while delete lock is held: %v", err)
	}
	assertWindowsTurnLockChild(t, dir, "missing")
}

func TestWindowsTurnLockProbeDoesNotCreateArtifacts(t *testing.T) {
	dir := t.TempDir()
	if TurnLockHeld(dir) {
		t.Fatal("empty bundle reported busy")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("probe changed the empty bundle: %v, %v", entries, err)
	}
}

func assertWindowsTurnLockChild(t *testing.T, dir, want string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWindowsTurnLockChild$")
	cmd.Env = append(os.Environ(), "CODDY_TEST_TURN_LOCK_DIR="+dir, "CODDY_TEST_TURN_LOCK_WANT="+want)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child check (%s): %v\n%s", want, err, output)
	}
}

func TestWindowsTurnLockChild(t *testing.T) {
	dir := os.Getenv("CODDY_TEST_TURN_LOCK_DIR")
	if dir == "" {
		return
	}
	want := os.Getenv("CODDY_TEST_TURN_LOCK_WANT")
	if got := TurnLockHeld(dir); got != (want == "busy") {
		t.Fatalf("TurnLockHeld = %v in state %s", got, want)
	}
	m := new(Manager)
	st := &State{ID: "different-id", SessionDir: dir}
	for _, acquire := range []func() (func(), error){
		func() (func(), error) { return m.acquirePromptTurnLock(st.ID, st) },
		func() (func(), error) { return m.acquireDeleteTurnLock(st.ID, dir) },
	} {
		unlock, err := acquire()
		if unlock != nil {
			unlock()
		}
		switch want {
		case "busy":
			if !errors.Is(err, ErrSessionTurnBusy) || unlock != nil {
				t.Fatalf("want busy, got %v", err)
			}
		case "missing":
			if !errors.Is(err, fs.ErrNotExist) || unlock != nil {
				t.Fatalf("want missing, got %v", err)
			}
			if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("acquisition recreated the bundle: %v", err)
			}
		case "idle":
			if err != nil || unlock == nil {
				t.Fatalf("want successful acquisition, got %v", err)
			}
		default:
			t.Fatalf("unknown test state %q", want)
		}
	}
}
