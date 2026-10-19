package gitws

import (
	"os"
	"path/filepath"
	"testing"
)

// EnsureLocalWorktree opens a worktree on a new branch at the caller's HEAD
// without a remote: a repository with no origin at all is enough, the
// .worktreeinclude files come along, and a second call finds the same
// worktree.
func TestEnsureLocalWorktreeStartsAtHeadWithoutARemote(t *testing.T) {
	if gitBin == "" {
		t.Skip("git binary not available")
	}
	main := normPath(t, t.TempDir())
	mustGit(t, main, "init", "-q", "-b", "main")
	head := commitFile(t, main, ".gitignore", ".env\n")
	writeFile(t, main, ".worktreeinclude", ".env\n")
	writeFile(t, main, ".env", "TOKEN=local\n")

	opened, err := EnsureLocalWorktree(main, "subagent/fixer-1a2b3c4d")
	if err != nil {
		t.Fatal(err)
	}
	if !opened.Created || opened.Path != filepath.Join(WorktreesRoot(main), "subagent-fixer-1a2b3c4d") {
		t.Fatalf("opened = %+v, want a new worktree under %s", opened, WorktreesRoot(main))
	}
	if got := mustGit(t, opened.Path, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD = %s, want the caller's %s", got, head)
	}
	if got := mustGit(t, opened.Path, "rev-parse", "--abbrev-ref", "HEAD"); got != "subagent/fixer-1a2b3c4d" {
		t.Fatalf("branch = %s", got)
	}
	if body, err := os.ReadFile(filepath.Join(opened.Path, ".env")); err != nil || string(body) != "TOKEN=local\n" {
		t.Fatalf(".env = %q, %v; want the main checkout's copy", body, err)
	}
	again, err := EnsureLocalWorktree(main, "subagent/fixer-1a2b3c4d")
	if err != nil || again.Created || again.Path != opened.Path {
		t.Fatalf("again = %+v, %v; want the same worktree found", again, err)
	}
}

// From a linked worktree the new branch starts at that worktree's HEAD, and
// the new worktree still sits under the main checkout's worktrees folder. A
// branch that is left from an earlier worktree is checked out again at its own
// tip.
func TestEnsureLocalWorktreeFromALinkedWorktree(t *testing.T) {
	main := initRepo(t)
	parent := openedWorktree(t, main, "feature/parent")
	tip := commitFile(t, parent.Path, "work.txt", "in progress\n")
	opened, err := EnsureLocalWorktree(parent.Path, "subagent/child")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(opened.Path) != WorktreesRoot(main) {
		t.Fatalf("path = %s, want it under %s", opened.Path, WorktreesRoot(main))
	}
	if got := mustGit(t, opened.Path, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("HEAD = %s, want the parent worktree's %s", got, tip)
	}
	own := commitFile(t, opened.Path, "child.txt", "the child's\n")
	mustGit(t, main, "worktree", "remove", opened.Path)
	again, err := EnsureLocalWorktree(parent.Path, "subagent/child")
	if err != nil || !again.Created {
		t.Fatalf("again = %+v, %v; want the worktree made again", again, err)
	}
	if got := mustGit(t, again.Path, "rev-parse", "HEAD"); got != own {
		t.Fatalf("HEAD = %s, want the branch's own tip %s", got, own)
	}
}

func TestEnsureLocalWorktreeRefusesABranchCheckedOutInTheMainCheckout(t *testing.T) {
	main := initRepo(t)
	if _, err := EnsureLocalWorktree(main, "main"); err == nil {
		t.Fatal("EnsureLocalWorktree(main) = nil error, want a refusal")
	}
}

func TestFolderInFindsTheSameFolderInTheWorktree(t *testing.T) {
	main := initRepo(t)
	commitFile(t, main, "README.md", "x\n")
	if err := os.MkdirAll(filepath.Join(main, "pkg", "util"), 0o755); err != nil {
		t.Fatal(err)
	}
	commitFile(t, main, filepath.Join("pkg", "util", "util.go"), "package util\n")
	if err := os.MkdirAll(filepath.Join(main, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	opened, err := EnsureLocalWorktree(main, "subagent/folder")
	if err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]string{
		main:                               opened.Path,
		filepath.Join(main, "pkg"):         filepath.Join(opened.Path, "pkg"),
		filepath.Join(main, "pkg", "util"): filepath.Join(opened.Path, "pkg", "util"),
		// Untracked: the worktree has no such folder.
		filepath.Join(main, "scratch"): opened.Path,
	} {
		if got := FolderIn(dir, opened.Path); got != want {
			t.Errorf("FolderIn(%s) = %s, want %s", dir, got, want)
		}
	}
}
