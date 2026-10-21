// Package gitwstest builds git repositories for tests in other packages: a
// project and a linked worktree of it, the way worktree_open lays them out.
// It runs the git binary and skips the test where there is none.
package gitwstest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// ProjectWithWorktree creates a repository with one empty commit and a linked
// worktree of it on a new branch under <main>/.coddy/worktrees/, and returns
// both roots. The user's git configuration is kept out of it.
func ProjectWithWorktree(t testing.TB) (main, worktree string) {
	t.Helper()
	main = Repository(t)
	worktree, err := AddWorktree(main)
	if err != nil {
		t.Fatal(err)
	}
	return main, worktree
}

// Repository creates a repository on main with one empty commit.
func Repository(t testing.TB) string {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	// Git records the physical path; keep derived fixture paths in the same
	// spelling when Windows gives TempDir an 8.3 user-directory name.
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}

// Init makes dir, which must exist, a repository on main with one empty
// commit. It is the fixture for a caller with no testing.TB (a godog step).
func Init(dir string) error {
	if err := Run(dir, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	return Run(dir, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-q", "--allow-empty", "-m", "init")
}

// AddWorktree adds a linked worktree of the repository at main on a new
// branch feature/login, under <main>/.coddy/worktrees/ as worktree_open lays
// it out, and returns its root.
func AddWorktree(main string) (string, error) {
	worktree := filepath.Join(main, ".coddy", "worktrees", "feature-login")
	return worktree, Run(main, "worktree", "add", "-q", "-b", "feature/login", worktree)
}

// Git runs git in dir and fails the test on an error.
func Git(t testing.TB, dir string, args ...string) {
	t.Helper()
	requireGit(t)
	if err := Run(dir, args...); err != nil {
		t.Fatal(err)
	}
}

// Run runs git in dir with the user's configuration kept out of it.
func Run(dir string, args ...string) error {
	git, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git binary not available: %w", err)
	}
	cmd := exec.Command(git, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	platform.AdaptCommand(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %w\n%s", args, err, out)
	}
	return nil
}

func requireGit(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available to build the fixture")
	}
}
