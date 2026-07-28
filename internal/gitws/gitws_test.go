package gitws

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func normPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return r
}

// initRepo creates a repo on branch "main" with one commit and a
// "feature/login" branch pointing at the same commit.
func initRepo(t *testing.T) string {
	t.Helper()
	if !GitAvailable() {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	mustGit(t, dir, "init", "-b", "main")
	mustGit(t, dir, "-c", "user.email=coddy@test", "-c", "user.name=coddy",
		"commit", "--allow-empty", "-m", "init")
	mustGit(t, dir, "branch", "feature/login")
	return normPath(t, dir)
}

func TestCloneAndPull(t *testing.T) {
	if !GitAvailable() {
		t.Skip("git binary not available")
	}
	// Source repo with a committed SKILL.md on main.
	src := t.TempDir()
	mustGit(t, src, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: demo\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, src, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "add", "SKILL.md")
	mustGit(t, src, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-m", "add skill")

	dest := filepath.Join(t.TempDir(), "clone")
	if err := Clone(src, "", dest); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Fatalf("cloned SKILL.md missing: %v", err)
	}
	// Pull is a no-op fast-forward here, but must not error on a clean clone.
	if err := Pull(dest); err != nil {
		t.Fatalf("Pull: %v", err)
	}
}

func TestDescribePlainFolder(t *testing.T) {
	dir := t.TempDir()
	info := Describe(dir)
	if info.IsGitRepo {
		t.Fatalf("plain folder reported as git repo: %+v", info)
	}
	if info.Path == "" {
		t.Fatal("expected Path to be set")
	}
	if info.IsWorktree {
		t.Fatal("plain folder cannot be a worktree")
	}
}

func TestDescribeRepo(t *testing.T) {
	dir := initRepo(t)
	info := Describe(dir)
	if !info.IsGitRepo {
		t.Fatalf("expected git repo: %+v", info)
	}
	if info.Branch != "main" {
		t.Fatalf("branch = %q, want main", info.Branch)
	}
	if normPath(t, info.RepoRoot) != dir {
		t.Fatalf("repo root = %q, want %q", info.RepoRoot, dir)
	}
	if !slices.Contains(info.Branches, "main") || !slices.Contains(info.Branches, "feature/login") {
		t.Fatalf("branches = %v, want main and feature/login", info.Branches)
	}
	if info.IsWorktree {
		t.Fatal("main checkout must not be flagged as worktree")
	}
	if len(info.Worktrees) != 1 || !info.Worktrees[0].Main {
		t.Fatalf("worktrees = %+v, want single main entry", info.Worktrees)
	}
}

func TestDescribeSubdirOfRepo(t *testing.T) {
	dir := initRepo(t)
	sub := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	info := Describe(sub)
	if !info.IsGitRepo {
		t.Fatal("subdir of a repo must report the repo")
	}
	if normPath(t, info.RepoRoot) != dir {
		t.Fatalf("repo root = %q, want %q", info.RepoRoot, dir)
	}
}

func TestCheckout(t *testing.T) {
	dir := initRepo(t)
	if err := Checkout(dir, "feature/login"); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if got := Describe(dir).Branch; got != "feature/login" {
		t.Fatalf("branch after checkout = %q", got)
	}
	if err := Checkout(dir, "no-such-branch"); err == nil {
		t.Fatal("expected error for unknown branch")
	}
}

func TestEnsureWorktree(t *testing.T) {
	dir := initRepo(t)
	root := t.TempDir()

	path, created, err := EnsureWorktree(dir, "feature/login", root)
	if err != nil {
		t.Fatalf("ensure worktree: %v", err)
	}
	if !created {
		t.Fatal("expected worktree to be created")
	}
	if !strings.HasPrefix(normPath(t, path), normPath(t, root)) {
		t.Fatalf("worktree path %q not under %q", path, root)
	}

	info := Describe(path)
	if !info.IsGitRepo || info.Branch != "feature/login" {
		t.Fatalf("worktree info = %+v", info)
	}
	if !info.IsWorktree {
		t.Fatal("linked worktree must be flagged IsWorktree")
	}
	if normPath(t, info.RepoRoot) != dir {
		t.Fatalf("worktree repo root = %q, want main root %q", info.RepoRoot, dir)
	}

	again, createdAgain, err := EnsureWorktree(dir, "feature/login", root)
	if err != nil {
		t.Fatalf("ensure worktree twice: %v", err)
	}
	if createdAgain {
		t.Fatal("second call must reuse the worktree")
	}
	if normPath(t, again) != normPath(t, path) {
		t.Fatalf("reused path %q != %q", again, path)
	}

	mainInfo := Describe(dir)
	found := false
	for _, wt := range mainInfo.Worktrees {
		if wt.Branch == "feature/login" && !wt.Main {
			found = true
		}
	}
	if !found {
		t.Fatalf("main repo worktree list misses the branch: %+v", mainInfo.Worktrees)
	}
}

func TestGitAvailable(t *testing.T) {
	if _, err := exec.LookPath("git"); err == nil && !GitAvailable() {
		t.Fatal("git is on PATH but GitAvailable is false")
	}
}

func TestBranchDirName(t *testing.T) {
	cases := map[string]string{
		"main":           "main",
		"feature/login":  "feature-login",
		"fix\\win":       "fix-win",
		"weird name:tag": "weird-name-tag",
	}
	for in, want := range cases {
		if got := BranchDirName(in); got != want {
			t.Fatalf("BranchDirName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCloneRejectsOptionLikeArgs(t *testing.T) {
	if !GitAvailable() {
		t.Skip("git binary not available")
	}
	dest := filepath.Join(t.TempDir(), "dest")
	// A URL or ref that starts with "-" must be rejected, not passed to git
	// where it would be parsed as a flag (option injection).
	if err := Clone("--upload-pack=touch pwned", "", dest); err == nil {
		t.Error("expected rejection of option-like url")
	}
	if err := Clone("https://example.com/x.git", "--foo", dest); err == nil {
		t.Error("expected rejection of option-like ref")
	}
}
