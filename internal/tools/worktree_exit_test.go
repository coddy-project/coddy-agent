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
	_, err := apptools.NewRegistry().Execute(context.Background(), apptools.ToolWorktreeOpen,
		fmt.Sprintf(`{"branch":%q,"base":"tip"}`, "feature/x"), s.env)
	if err == nil || !strings.Contains(err.Error(), "tip") {
		t.Fatalf("worktree_open base tip = %v, want a refusal naming it", err)
	}
	stays(t, s, s.root)
}

// Two chats can share one worktree (a branch has one): removing it from under
// the other one would strand it, so the call is refused while another live
// session works there.
func TestWorktreeExitRefusesToRemoveAWorktreeAnotherSessionWorksIn(t *testing.T) {
	s := openedNavigation(t, "feature/shared")
	wt := s.worktrees["feature/shared"]
	s.env.SessionsInWorkspace = func(dir string) []string {
		if session.SameWorkspacePath(dir, wt) {
			return []string{"sess_other"}
		}
		return nil
	}
	if err := exitWorktree(s, `{"action":"remove","discard_changes":true}`); err == nil || !strings.Contains(err.Error(), "sess_other") {
		t.Fatalf("remove under another session = %v, want a refusal naming it", err)
	}
	stays(t, s, wt)
}

func TestWorktreeExitRefusesToRemoveAWorktreeAnotherSessionsTaskRunsIn(t *testing.T) {
	s := openedNavigation(t, "feature/their-server")
	wt := s.worktrees["feature/their-server"]
	pool := bgtask.New(bgtask.Config{})
	t.Cleanup(func() { pool.StopSession("sess_other") })
	s.env.Background = pool
	if _, err := pool.Launch(bgtask.Spec{SessionID: "sess_other", Kind: bgtask.KindServer, Label: "preview", CWD: wt},
		func(string, io.Writer) (bgtask.Handle, error) { return &blockingHandle{done: make(chan struct{})}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := exitWorktree(s, `{"action":"remove"}`); err == nil || !strings.Contains(err.Error(), "background task") {
		t.Fatalf("remove under another session's task = %v, want a refusal", err)
	}
	stays(t, s, wt)
}

// What git would refuse only after the session moved - a locked worktree, one
// with initialised submodules - is refused before, so the session stays.
func TestWorktreeExitRefusesALockedWorktreeBeforeMoving(t *testing.T) {
	s := openedNavigation(t, "feature/locked")
	wt := s.worktrees["feature/locked"]
	if _, err := s.git(s.root, "worktree", "lock", wt); err != nil {
		t.Fatal(err)
	}
	if err := exitWorktree(s, `{"action":"remove","discard_changes":true}`); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("remove of a locked worktree = %v, want a refusal", err)
	}
	stays(t, s, wt)
}

// A chat whose worktree folder is gone - removed by hand, or by another chat
// - comes back to the main checkout that held it instead of being stuck.
func TestWorktreeExitBringsAChatBackFromAWorktreeThatIsGone(t *testing.T) {
	s := openedNavigation(t, "feature/vanished")
	wt := s.worktrees["feature/vanished"]
	// Deleted by hand: git still lists it.
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	if err := exitWorktree(s, `{}`); err != nil {
		t.Fatalf("leave a vanished worktree = %v", err)
	}
	if err := s.inMainCheckout(); err != nil {
		t.Fatal(err)
	}
	// git forgot it on the way, so the branch opens in a worktree again.
	if err := s.worksInWorktree("feature/vanished"); err != nil {
		t.Fatalf("reopen the branch of a vanished worktree = %v", err)
	}
	if _, err := os.Stat(s.worktrees["feature/vanished"]); err != nil {
		t.Fatalf("the reopened worktree has no folder: %v", err)
	}
}

// Removing with discard_changes keeps the dropped work as a snapshot, and the
// answer names it.
func TestWorktreeExitNamesTheSnapshotOfDiscardedWork(t *testing.T) {
	s := openedNavigation(t, "feature/snapshot")
	wt := s.worktrees["feature/snapshot"]
	if err := os.WriteFile(filepath.Join(wt, "draft.txt"), []byte("half done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := apptools.NewRegistry().Execute(context.Background(), apptools.ToolWorktreeExit, `{"action":"remove","discard_changes":true}`, s.env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"snapshot":"refs/coddy/worktree-snapshots/`) {
		t.Fatalf("answer %s does not name the snapshot", out)
	}
}

// Leaving with the default action never refuses: a worktree that has
// something of its own, or someone still in it, stays, and the answer says
// why, while the session goes back all the same.
func TestWorktreeExitAutoKeepsWhatItCannotRemove(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, s *worktreeNavigationState, wt string)
		want    string
	}{
		{
			name: "uncommitted work",
			prepare: func(t *testing.T, _ *worktreeNavigationState, wt string) {
				if err := os.WriteFile(filepath.Join(wt, "draft.txt"), []byte("not yet\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "uncommitted",
		},
		{
			name: "another session",
			prepare: func(_ *testing.T, s *worktreeNavigationState, wt string) {
				s.env.SessionsInWorkspace = func(dir string) []string {
					if session.SameWorkspacePath(dir, wt) {
						return []string{"sess_other"}
					}
					return nil
				}
			},
			want: "sess_other",
		},
		{
			name: "a lock",
			prepare: func(t *testing.T, s *worktreeNavigationState, wt string) {
				if _, err := s.git(s.root, "worktree", "lock", wt); err != nil {
					t.Fatal(err)
				}
			},
			want: "locked",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openedNavigation(t, "feature/busy")
			wt := s.worktrees["feature/busy"]
			tc.prepare(t, s, wt)
			if err := s.leaves(); err != nil {
				t.Fatalf("auto exit = %v, want the session back and the worktree kept", err)
			}
			if err := s.inMainCheckout(); err != nil {
				t.Fatal(err)
			}
			if s.left.Removed || !strings.Contains(s.left.Kept, tc.want) {
				t.Fatalf("answer = %+v, want the worktree kept for %q", s.left, tc.want)
			}
			if _, err := os.Stat(wt); err != nil {
				t.Fatalf("the worktree went: %v", err)
			}
		})
	}
}

// A worktree Coddy did not open is never removed by the default action, empty
// as it is.
func TestWorktreeExitAutoKeepsAWorktreeCoddyDidNotOpen(t *testing.T) {
	s := &worktreeNavigationState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(s.root)
		_ = os.RemoveAll(s.root + "-origin.git")
		_ = os.RemoveAll(s.root + "-beside")
	})
	beside := s.root + "-beside"
	if _, err := s.git(s.root, "worktree", "add", "-q", "-b", "feature/beside", beside, "HEAD"); err != nil {
		t.Fatal(err)
	}
	if err := s.execute(apptools.ToolWorktreeOpen, fmt.Sprintf(`{"path":%q}`, beside)); err != nil {
		t.Fatal(err)
	}
	if err := s.leaves(); err != nil {
		t.Fatal(err)
	}
	if s.left.Removed || !strings.Contains(s.left.Kept, "not a worktree Coddy opened") {
		t.Fatalf("answer = %+v, want the worktree kept as not Coddy's", s.left)
	}
	if _, err := os.Stat(beside); err != nil {
		t.Fatalf("the worktree went: %v", err)
	}
}
