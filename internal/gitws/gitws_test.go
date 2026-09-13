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
	root := WorktreesRoot(dir)

	path, created, err := EnsureWorktree(dir, "feature/login")
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

	again, createdAgain, err := EnsureWorktree(dir, "feature/login")
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

// The worktrees root is a fixed spot inside the repository, the way Claude Code
// and Codex keep theirs, so nothing lands next to the project's own folders.
func TestWorktreesRoot(t *testing.T) {
	want := filepath.Join("/repo", ".coddy", "worktrees")
	if got := WorktreesRoot("/repo"); got != want {
		t.Fatalf("WorktreesRoot = %q, want %q", got, want)
	}
}

// A worktree inside the repository must not turn up as an untracked folder:
// the root carries its own ignore file, so no operator has to add one.
func TestEnsureWorktreeIsIgnoredByGit(t *testing.T) {
	dir := initRepo(t)

	if _, _, err := EnsureWorktree(dir, "feature/login"); err != nil {
		t.Fatalf("ensure worktree: %v", err)
	}
	if dirty := mustGit(t, dir, "status", "--porcelain"); dirty != "" {
		t.Fatalf("repository is not clean after adding a worktree:\n%s", dirty)
	}
	ignore := filepath.Join(WorktreesRoot(dir), ".gitignore")
	body, err := os.ReadFile(ignore)
	if err != nil {
		t.Fatalf("read %s: %v", ignore, err)
	}
	if strings.TrimSpace(string(body)) != "*" {
		t.Fatalf("ignore file = %q, want \"*\"", string(body))
	}
}

// `git clean -xdf` in the main checkout deletes the ignore file and keeps the
// worktrees (measured on git 2.47), so a worktree that is only ever reused
// would stay visible in git status forever. Reuse restores the file.
func TestEnsureWorktreeRestoresIgnoreFileOnReuse(t *testing.T) {
	dir := initRepo(t)
	if _, _, err := EnsureWorktree(dir, "feature/login"); err != nil {
		t.Fatalf("ensure worktree: %v", err)
	}
	ignore := filepath.Join(WorktreesRoot(dir), ".gitignore")
	if err := os.Remove(ignore); err != nil {
		t.Fatalf("remove ignore: %v", err)
	}
	if dirty := mustGit(t, dir, "status", "--porcelain"); dirty == "" {
		t.Fatal("removing the ignore file should have exposed the worktree")
	}

	if _, created, err := EnsureWorktree(dir, "feature/login"); err != nil {
		t.Fatalf("ensure worktree again: %v", err)
	} else if created {
		t.Fatal("the second call must reuse the worktree, not create one")
	}
	if _, err := os.Stat(ignore); err != nil {
		t.Fatalf("ignore file was not restored: %v", err)
	}
	if dirty := mustGit(t, dir, "status", "--porcelain"); dirty != "" {
		t.Fatalf("repository is not clean after reuse:\n%s", dirty)
	}
}

// `.coddy` is repository content, so a checkout can ship it as a symlink
// pointing anywhere. A worktree must never be written through it.
func TestEnsureWorktreeRefusesRootOutsideTheCheckout(t *testing.T) {
	dir := initRepo(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, ".coddy")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, _, err := EnsureWorktree(dir, "feature/login"); err == nil {
		t.Fatal("expected a worktrees root outside the checkout to be refused")
	}
	if entries, err := os.ReadDir(filepath.Join(outside, "worktrees")); err == nil && len(entries) > 0 {
		t.Fatalf("wrote through the symlink: %v", entries)
	}
	for _, wt := range listWorktrees(dir) {
		if !wt.Main {
			t.Fatalf("a worktree was created at %q", wt.Path)
		}
	}
}

// A branch name that looks like an option must never reach git as one:
// `git worktree add <path> --detach` is accepted by git and quietly builds a
// detached worktree nobody asked for.
func TestEnsureWorktreeRejectsOptionLikeBranch(t *testing.T) {
	dir := initRepo(t)

	if _, _, err := EnsureWorktree(dir, "--detach"); err == nil {
		t.Fatal("expected rejection of an option-like branch name")
	}
	for _, wt := range listWorktrees(dir) {
		if !wt.Main {
			t.Fatalf("an option-like branch created a worktree at %q", wt.Path)
		}
	}
}

// A name that sanitises down to nothing would put the worktree at the root
// itself; refuse it rather than let git explain it.
func TestEnsureWorktreeRejectsUnusableBranchName(t *testing.T) {
	dir := initRepo(t)

	if _, _, err := EnsureWorktree(dir, "..."); err == nil {
		t.Fatal("expected rejection of a branch name with no usable directory name")
	}
	if entries, err := os.ReadDir(WorktreesRoot(dir)); err == nil && len(entries) > 0 {
		t.Fatalf("worktrees root was populated: %v", entries)
	}
}

// An ignore file the operator wrote is theirs; ensuring a worktree must not
// rewrite it, even when what it says is not what Coddy would have written.
func TestEnsureWorktreeKeepsExistingIgnoreFile(t *testing.T) {
	dir := initRepo(t)
	root := WorktreesRoot(dir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	ignore := filepath.Join(root, ".gitignore")
	const operatorRule = "# mine\nbuild/\n"
	if err := os.WriteFile(ignore, []byte(operatorRule), 0o644); err != nil {
		t.Fatalf("write ignore: %v", err)
	}

	if _, _, err := EnsureWorktree(dir, "feature/login"); err != nil {
		t.Fatalf("ensure worktree: %v", err)
	}
	body, err := os.ReadFile(ignore)
	if err != nil {
		t.Fatalf("read ignore: %v", err)
	}
	if string(body) != operatorRule {
		t.Fatalf("ignore file was rewritten: %q", string(body))
	}
	// The trade-off of leaving it alone: a file that does not ignore the
	// worktrees leaves them visible, and that is the operator's call.
	if dirty := mustGit(t, dir, "status", "--porcelain"); dirty == "" {
		t.Fatal("an ignore file that ignores nothing should leave the worktree visible")
	}
}

// A worktree asked for from inside a linked worktree belongs to the main
// checkout's root, not to a nested one below the linked tree.
func TestEnsureWorktreeFromLinkedWorktree(t *testing.T) {
	dir := initRepo(t)
	first, _, err := EnsureWorktree(dir, "feature/login")
	if err != nil {
		t.Fatalf("ensure first worktree: %v", err)
	}
	mustGit(t, dir, "branch", "feature/logout")

	second, _, err := EnsureWorktree(first, "feature/logout")
	if err != nil {
		t.Fatalf("ensure second worktree: %v", err)
	}
	want := filepath.Join(WorktreesRoot(dir), "feature-logout")
	if normPath(t, second) != normPath(t, want) {
		t.Fatalf("second worktree = %q, want %q", second, want)
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
