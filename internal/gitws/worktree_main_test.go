package gitws

import (
	"os"
	"path/filepath"
	"testing"
)

// A folder of a linked worktree answers to the same folder of the main
// checkout: what is approved, shared or scheduled for the project there holds
// in every worktree of it.
func TestMainCheckoutFolderOfALinkedWorktree(t *testing.T) {
	main := initRepo(t)
	wt := filepath.Join(WorktreesRoot(main), "feature-login")
	mustGit(t, main, "worktree", "add", "-q", wt, "feature/login")
	if err := os.MkdirAll(filepath.Join(wt, "pkg", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]string{
		wt:                                 main,
		filepath.Join(wt, "pkg", "api"):    filepath.Join(main, "pkg", "api"),
		filepath.Join(wt, "not", "yet"):    filepath.Join(main, "not", "yet"),
		filepath.Join(wt, ".coddy", "mcp"): filepath.Join(main, ".coddy", "mcp"),
	} {
		got, ok := MainCheckoutFolder(dir)
		if !ok || !samePath(got, want) {
			t.Errorf("MainCheckoutFolder(%s) = %q, %t; want %q", dir, got, ok, want)
		}
	}
}

// A worktree outside the main checkout - the layout git worktree add makes by
// default - answers the same way.
func TestMainCheckoutFolderOfAWorktreeBesideTheCheckout(t *testing.T) {
	main := initRepo(t)
	wt := filepath.Join(t.TempDir(), "login")
	mustGit(t, main, "worktree", "add", "-q", wt, "feature/login")
	if got, ok := MainCheckoutFolder(wt); !ok || !samePath(got, main) {
		t.Fatalf("MainCheckoutFolder(%s) = %q, %t; want %q", wt, got, ok, main)
	}
}

// Only a linked worktree has a main checkout of its own to answer to.
func TestMainCheckoutFolderIsNoneOutsideALinkedWorktree(t *testing.T) {
	main := initRepo(t)
	nested := filepath.Join(main, "vendor", "dep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, nested, "init", "-q")
	wt := filepath.Join(WorktreesRoot(main), "feature-login")
	mustGit(t, main, "worktree", "add", "-q", wt, "feature/login")
	inWorktree := filepath.Join(wt, "vendor", "dep")
	if err := os.MkdirAll(inWorktree, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, inWorktree, "init", "-q")
	for _, dir := range []string{
		main,                          // the main checkout itself
		filepath.Join(main, "pkg"),    // a folder of it
		nested,                        // a repository of its own inside it
		inWorktree,                    // and one inside a worktree
		t.TempDir(),                   // a plain folder
		filepath.Join(main, ".git"),   // the git dir
		filepath.Join(wt, "..", ".."), // the worktrees root, inside the main checkout
	} {
		if got, ok := MainCheckoutFolder(dir); ok {
			t.Errorf("MainCheckoutFolder(%s) = %q, want none", dir, got)
		}
	}
}

// A .git file is plain text anyone can write: a folder that only claims to be
// a worktree of a repository - its .git file names the repository's worktree
// storage, but git never registered the folder there - does not borrow the
// repository's identity, and so none of what was approved for it.
func TestMainCheckoutFolderNeedsTheRepositoryToNameTheWorktreeBack(t *testing.T) {
	main := initRepo(t)
	wt := filepath.Join(WorktreesRoot(main), "feature-login")
	mustGit(t, main, "worktree", "add", "-q", wt, "feature/login")
	pointer, err := os.ReadFile(filepath.Join(wt, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	impostor := t.TempDir()
	if err := os.WriteFile(filepath.Join(impostor, ".git"), pointer, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := MainCheckoutFolder(impostor); ok {
		t.Fatalf("a copied .git file made %s answer to %q", impostor, got)
	}
	// Nor does a pointer at storage that is not a worktree's at all.
	if err := os.WriteFile(filepath.Join(impostor, ".git"), []byte("gitdir: "+filepath.Join(main, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := MainCheckoutFolder(impostor); ok {
		t.Fatalf("a .git file naming the main git dir made %s answer to %q", impostor, got)
	}
}

// Reading files is all it takes: a host without the git binary answers the
// same.
func TestMainCheckoutFolderNeedsNoGitBinary(t *testing.T) {
	main := initRepo(t)
	wt := filepath.Join(WorktreesRoot(main), "feature-login")
	mustGit(t, main, "worktree", "add", "-q", wt, "feature/login")
	hideGitBinary(t)
	if got, ok := MainCheckoutFolder(wt); !ok || !samePath(got, main) {
		t.Fatalf("without git MainCheckoutFolder(%s) = %q, %t; want %q", wt, got, ok, main)
	}
}

// The answer never leaves the main checkout. A folder that is a link in the
// main checkout's tree - committed there, while the worktree's branch has a
// real folder of that name - would otherwise lend the worktree the identity
// of wherever the link points.
func TestMainCheckoutFolderNeverFollowsALinkOutOfTheMainCheckout(t *testing.T) {
	main := initRepo(t)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(main, "pkg")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	wt := filepath.Join(WorktreesRoot(main), "feature-login")
	mustGit(t, main, "worktree", "add", "-q", wt, "feature/login")
	inWorktree := filepath.Join(wt, "pkg")
	if err := os.MkdirAll(inWorktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := MainCheckoutFolder(inWorktree); ok {
		t.Fatalf("MainCheckoutFolder(%s) = %q through a link out of the main checkout, want none", inWorktree, got)
	}
}

// A tree that brings its own repository storage - an extracted archive, say,
// with a .git file, the storage it names and a link to another project -
// answers at most with a folder of that storage's checkout, never with the
// folder the link points at.
func TestMainCheckoutFolderOfAnArchiveWithItsOwnStorageStaysInsideIt(t *testing.T) {
	victim := t.TempDir()
	evil := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(evil, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".git", "gitdir: v/.git/worktrees/x\n")
	write("v/.git/worktrees/x/commondir", "../..\n")
	write("v/.git/worktrees/x/gitdir", "../../../../.git\n")
	write("v/.git/HEAD", "ref: refs/heads/main\n")
	if err := os.Symlink(victim, filepath.Join(evil, "v", "pkg")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(evil, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := MainCheckoutFolder(filepath.Join(evil, "pkg")); ok {
		t.Fatalf("an archive's own storage and a link made it answer %q, want none", got)
	}
}

// A bare repository has worktrees but no main checkout, wherever its storage
// lies: one cloned into <folder>/.git does not make <folder> a checkout.
func TestMainCheckoutFolderIsNoneForABareRepositoryNamedDotGit(t *testing.T) {
	source := initRepo(t)
	proj := t.TempDir()
	mustGit(t, proj, "clone", "-q", "--bare", source, filepath.Join(proj, ".git"))
	wt := filepath.Join(proj, "login")
	mustGit(t, proj, "worktree", "add", "-q", wt, "feature/login")
	if got, ok := MainCheckoutFolder(wt); ok {
		t.Fatalf("a worktree of a bare repository answered %q, want none", got)
	}
}
