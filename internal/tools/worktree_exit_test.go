package tools_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	apptools "github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// openedNavigation is a session moved into a worktree for branch, the way
// the navigation feature sets one up.
func openedNavigation(t *testing.T, branch string) *worktreeNavigationState {
	t.Helper()
	s := &worktreeNavigationState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(s.root)
		_ = os.RemoveAll(s.root + "-origin.git")
	})
	if err := s.worksInWorktree(branch); err != nil {
		t.Fatal(err)
	}
	return s
}

func exitWorktree(s *worktreeNavigationState, args string) error {
	_, err := apptools.NewRegistry().Execute(context.Background(), apptools.ToolWorktreeExit, args, s.env)
	return err
}

// stays fails unless the session is still where the refused call found it.
func stays(t *testing.T, s *worktreeNavigationState, want string) {
	t.Helper()
	if cwd := s.state.GetCWD(); !session.SameWorkspacePath(cwd, want) {
		t.Fatalf("session moved to %q on a refused call, want %q", cwd, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("%s is gone after a refused call: %v", want, err)
	}
}

func TestWorktreeExitFromTheMainCheckoutIsRefused(t *testing.T) {
	s := &worktreeNavigationState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(s.root)
		_ = os.RemoveAll(s.root + "-origin.git")
	})
	if err := exitWorktree(s, `{}`); err == nil || !strings.Contains(err.Error(), "main checkout") {
		t.Fatalf("worktree_exit in the main checkout = %v, want a refusal naming it", err)
	}
	stays(t, s, s.root)
}

func TestWorktreeExitRefusesToRemoveUncommittedWorkAndStaysPut(t *testing.T) {
	s := openedNavigation(t, "feature/draft")
	wt := s.worktrees["feature/draft"]
	if err := os.WriteFile(filepath.Join(wt, "draft.txt"), []byte("not committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := exitWorktree(s, `{"action":"remove"}`); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("remove over uncommitted work = %v, want a refusal", err)
	}
	stays(t, s, wt)
	// Asked outright, the changes go and the session is back.
	if err := exitWorktree(s, `{"action":"remove","discard_changes":true}`); err != nil {
		t.Fatalf("remove with discard_changes = %v", err)
	}
	if err := s.inMainCheckout(); err != nil {
		t.Fatal(err)
	}
	if err := s.worktreeGone("feature/draft"); err != nil {
		t.Fatal(err)
	}
}

// blockingHandle is a background task that runs until released.
type blockingHandle struct{ done chan struct{} }

func (h *blockingHandle) Wait() (int, error) { <-h.done; return 0, nil }
func (h *blockingHandle) Stop(time.Duration) error {
	select {
	case <-h.done:
	default:
		close(h.done)
	}
	return nil
}
func (h *blockingHandle) PID() int                    { return 0 }
func (h *blockingHandle) ProcessStartedAt() time.Time { return time.Time{} }

func TestWorktreeExitRefusesToRemoveAWorktreeABackgroundTaskRunsIn(t *testing.T) {
	s := openedNavigation(t, "feature/server")
	wt := s.worktrees["feature/server"]
	pool := bgtask.New(bgtask.Config{})
	t.Cleanup(func() { pool.StopSession(s.state.ID) })
	s.env.Background = pool
	if _, err := pool.Launch(bgtask.Spec{SessionID: s.state.ID, Kind: bgtask.KindServer, Label: "preview", CWD: filepath.Join(wt, "site")},
		func(string, io.Writer) (bgtask.Handle, error) { return &blockingHandle{done: make(chan struct{})}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := exitWorktree(s, `{"action":"remove"}`); err == nil || !strings.Contains(err.Error(), "background task") {
		t.Fatalf("remove under a running task = %v, want a refusal naming it", err)
	}
	stays(t, s, wt)
	// Keeping the worktree is no trouble for the task.
	if err := exitWorktree(s, `{}`); err != nil {
		t.Fatalf("leave and keep = %v", err)
	}
	if err := s.inMainCheckout(); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeExitRejectsArgumentsThatDoNotMakeACall(t *testing.T) {
	s := openedNavigation(t, "feature/args")
	wt := s.worktrees["feature/args"]
	for _, args := range []string{`{"action":"drop"}`, `{"discard_changes":true}`, `{"action":"keep","discard_changes":true}`} {
		if err := exitWorktree(s, args); err == nil {
			t.Fatalf("worktree_exit %s succeeded", args)
		}
		stays(t, s, wt)
	}
}

func TestWorktreeCreateRejectsAnUnknownBase(t *testing.T) {
	s := &worktreeNavigationState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(s.root)
		_ = os.RemoveAll(s.root + "-origin.git")
	})
	_, err := apptools.NewRegistry().Execute(context.Background(), apptools.ToolWorktreeCreate,
		fmt.Sprintf(`{"branch":%q,"base":"tip"}`, "feature/x"), s.env)
	if err == nil || !strings.Contains(err.Error(), "tip") {
		t.Fatalf("worktree_create base tip = %v, want a refusal naming it", err)
	}
	stays(t, s, s.root)
}
