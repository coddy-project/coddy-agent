package gitws

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// A new worktree gets a copy of the main checkout's ignored files that
// .worktreeinclude names - a .env, a local .coddy/mcp.json - and nothing
// else: not a file git tracks (the branch has its own), not an untracked file
// git does not ignore, not one the include file does not name.
func TestANewWorktreeGetsTheIgnoredFilesTheIncludeFileNames(t *testing.T) {
	main := initRepo(t)
	commitFile(t, main, ".gitignore", ".env\n.coddy/\nbuild/\nsecrets/\n")
	commitFile(t, main, "README.md", "readme\n")
	writeFile(t, main, ".worktreeinclude", "# what a new worktree needs\n.env\n.coddy/mcp.json\nsecrets/\nREADME.md\nnotes.txt\n")
	writeFile(t, main, ".env", "TOKEN=local\n")
	if err := os.Chmod(filepath.Join(main, ".env"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, main, ".coddy/mcp.json", `{"mcpServers":{}}`)
	writeFile(t, main, "secrets/key.pem", "key\n")
	writeFile(t, main, "secrets/nested/cert.pem", "cert\n")
	writeFile(t, main, "build/out.bin", "not named\n")
	writeFile(t, main, "notes.txt", "untracked, not ignored\n")

	opened, err := EnsureWorktreeDetailed(main, "feature/login", BaseDefault)
	if err != nil {
		t.Fatal(err)
	}
	if !opened.Created || opened.IncludeErr != nil {
		t.Fatalf("opened = %+v, want a created worktree and no include error", opened)
	}
	want := []string{".coddy/mcp.json", ".env", "secrets/key.pem", "secrets/nested/cert.pem"}
	if !slices.Equal(opened.Included, want) {
		t.Fatalf("included = %v, want %v", opened.Included, want)
	}
	for _, rel := range want {
		got, err := os.ReadFile(filepath.Join(opened.Path, filepath.FromSlash(rel)))
		src, _ := os.ReadFile(filepath.Join(main, filepath.FromSlash(rel)))
		if err != nil || string(got) != string(src) {
			t.Errorf("%s in the worktree = %q, %v; want the main checkout's", rel, got, err)
		}
	}
	for _, rel := range []string{"build/out.bin", "notes.txt"} {
		if _, err := os.Stat(filepath.Join(opened.Path, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s was copied (%v), want only what the include file names and git ignores", rel, err)
		}
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(filepath.Join(opened.Path, ".env")); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf(".env mode = %v, %v; want the source's 0600", fi.Mode(), err)
		}
	}
	// An existing worktree is opened again, not filled again.
	again, err := EnsureWorktreeDetailed(main, "feature/login", BaseDefault)
	if err != nil || again.Created || len(again.Included) != 0 {
		t.Fatalf("reopened = %+v, %v; want the existing worktree untouched", again, err)
	}
}

// What the worktree already has is never overwritten, and a link is not
// followed: only regular files are copied.
func TestCopyWorktreeIncludesKeepsWhatIsThereAndSkipsLinks(t *testing.T) {
	main := initRepo(t)
	commitFile(t, main, ".gitignore", ".env\nlinked\n")
	writeFile(t, main, ".worktreeinclude", ".env\nlinked\n")
	writeFile(t, main, ".env", "from main\n")
	outside := filepath.Join(t.TempDir(), "secret")
	writeFile(t, filepath.Dir(outside), "secret", "outside the checkout\n")
	linked := true
	if err := os.Symlink(outside, filepath.Join(main, "linked")); err != nil {
		linked = false
	}
	wt := filepath.Join(WorktreesRoot(main), "feature-login")
	mustGit(t, main, "worktree", "add", "-q", wt, "feature/login")
	writeFile(t, wt, ".env", "the worktree's own\n")
	included, err := CopyWorktreeIncludes(main, wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(included) != 0 {
		t.Fatalf("included = %v, want nothing: .env is there and the link is no regular file", included)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, ".env")); string(got) != "the worktree's own\n" {
		t.Fatalf(".env = %q, want the worktree's own", got)
	}
	if linked {
		if _, err := os.Lstat(filepath.Join(wt, "linked")); !os.IsNotExist(err) {
			t.Fatalf("the link was copied: %v", err)
		}
	}
}

// Without the include file nothing is copied and nothing fails.
func TestCopyWorktreeIncludesWithoutTheFile(t *testing.T) {
	main := initRepo(t)
	commitFile(t, main, ".gitignore", ".env\n")
	writeFile(t, main, ".env", "x\n")
	wt := filepath.Join(WorktreesRoot(main), "feature-login")
	mustGit(t, main, "worktree", "add", "-q", wt, "feature/login")
	included, err := CopyWorktreeIncludes(main, wt)
	if err != nil || len(included) != 0 {
		t.Fatalf("CopyWorktreeIncludes = %v, %v; want nothing", included, err)
	}
}
