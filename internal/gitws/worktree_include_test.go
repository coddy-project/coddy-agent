package gitws

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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
	// The branch carries the ignore rules the main checkout has.
	mustGit(t, main, "branch", "-f", "feature/login")
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

// The copy has the source's mode bits as they are, not as the umask leaves
// them.
func TestCopyWorktreeIncludesKeepsTheModePastTheUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits")
	}
	main := initRepo(t)
	commitFile(t, main, ".gitignore", ".env\n")
	writeFile(t, main, ".worktreeinclude", ".env\n")
	writeFile(t, main, ".env", "shared with the group\n")
	if err := os.Chmod(filepath.Join(main, ".env"), 0o660); err != nil {
		t.Fatal(err)
	}
	mustGit(t, main, "branch", "-f", "feature/login")
	wt := filepath.Join(WorktreesRoot(main), "feature-login")
	mustGit(t, main, "worktree", "add", "-q", wt, "feature/login")
	if _, err := CopyWorktreeIncludes(main, wt); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(wt, ".env")); err != nil || fi.Mode().Perm() != 0o660 {
		t.Fatalf(".env mode = %v, %v; want 0660 whatever the umask", fi.Mode(), err)
	}
}

// A copy edited in the worktree, or a file the include file names that only
// the worktree has, is work of its own; a copy left as it was is not.
func TestOwnWorkCountsChangedIncludeCopies(t *testing.T) {
	main := initRepo(t)
	commitFile(t, main, ".gitignore", ".env\nlocal/\nbuild/\n")
	mustGit(t, main, "push", "-q", "origin", "main")
	writeFile(t, main, ".worktreeinclude", ".env\nlocal/\n")
	writeFile(t, main, ".env", "TOKEN=main\n")
	opened, err := EnsureWorktreeDetailed(main, "feature/env", BaseDefault)
	if err != nil || len(opened.Included) != 1 {
		t.Fatalf("opened = %+v, %v; want .env copied", opened, err)
	}
	wt, err := WorktreeOf(opened.Path)
	if err != nil {
		t.Fatal(err)
	}
	own := func(wantEmpty bool, want, why string) {
		t.Helper()
		got, err := OwnWork(wt)
		if err != nil {
			t.Fatalf("%s: %v", why, err)
		}
		if wantEmpty != (got == "") || !strings.Contains(got, want) {
			t.Fatalf("%s: OwnWork = %q, want %q", why, got, want)
		}
	}
	writeFile(t, opened.Path, "build/out.bin", "ignored output\n")
	own(true, "", "an untouched copy and ignored output")
	writeFile(t, opened.Path, ".env", "TOKEN=edited\n")
	own(false, ".env", "an edited copy")
	writeFile(t, opened.Path, ".env", "TOKEN=main\n")
	writeFile(t, opened.Path, "local/notes.md", "only here\n")
	own(false, "local/notes.md", "a named file only the worktree has")
}

// A folder the branch tracks as a link is not followed: the copy would land
// where git does not ignore it. Folders the copy makes keep the source
// folder's mode.
func TestCopyWorktreeIncludesSkipsLinkedFoldersAndKeepsFolderModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX links and mode bits")
	}
	main := initRepo(t)
	if err := os.MkdirAll(filepath.Join(main, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	commitFile(t, main, "public/README", "public\n")
	commitFile(t, main, ".gitignore", "local/.env\nprivate/\n")
	mustGit(t, main, "branch", "-f", "feature/link")
	wt := filepath.Join(WorktreesRoot(main), "feature-link")
	mustGit(t, main, "worktree", "add", "-q", wt, "feature/link")
	// The branch's own checkout has local as a link to public.
	if err := os.Symlink("public", filepath.Join(wt, "local")); err != nil {
		t.Skip("symlinks not available")
	}
	writeFile(t, main, ".worktreeinclude", "local/.env\nprivate/\n")
	writeFile(t, main, "local/.env", "TOKEN=secret\n")
	writeFile(t, main, "private/config.env", "x\n")
	if err := os.Chmod(filepath.Join(main, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	included, err := CopyWorktreeIncludes(main, wt)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(included, []string{"private/config.env"}) {
		t.Fatalf("included = %v, want only private/config.env", included)
	}
	if _, err := os.Lstat(filepath.Join(wt, "public", ".env")); !os.IsNotExist(err) {
		t.Fatalf("the copy went through the linked folder: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(wt, "private")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("private/ mode = %v, %v; want the source's 0700", fi.Mode(), err)
	}
}

// A branch whose .gitignore does not cover a file gets no copy of it: git
// would show the copy as a new file there, one `git add -A` from a commit.
func TestCopyWorktreeIncludesSkipsWhatTheBranchDoesNotIgnore(t *testing.T) {
	main := initRepo(t) // feature/login is older than the ignore rule below
	commitFile(t, main, ".gitignore", ".env\n.worktreeinclude\n")
	writeFile(t, main, ".worktreeinclude", ".env\n")
	writeFile(t, main, ".env", "TOKEN=secret\n")
	opened, err := EnsureWorktreeDetailed(main, "feature/login", BaseDefault)
	if err != nil {
		t.Fatal(err)
	}
	if len(opened.Included) != 0 || opened.IncludeErr != nil {
		t.Fatalf("opened = %+v, want nothing copied into a branch that does not ignore .env", opened)
	}
	if status := mustGit(t, opened.Path, "status", "--porcelain"); status != "" {
		t.Fatalf("the worktree shows %q, want a clean status", status)
	}
}

// A read-only folder is filled first and made read-only after.
func TestCopyWorktreeIncludesFillsAReadOnlyFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits")
	}
	main := initRepo(t)
	commitFile(t, main, ".gitignore", "local/\n")
	mustGit(t, main, "branch", "-f", "feature/login")
	writeFile(t, main, ".worktreeinclude", "local/\n")
	writeFile(t, main, "local/conf.txt", "conf\n")
	if err := os.Chmod(filepath.Join(main, "local"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(main, "local"), 0o755) })
	wt := filepath.Join(WorktreesRoot(main), "feature-login")
	mustGit(t, main, "worktree", "add", "-q", wt, "feature/login")
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(wt, "local"), 0o755) })
	included, err := CopyWorktreeIncludes(main, wt)
	if err != nil || !slices.Equal(included, []string{"local/conf.txt"}) {
		t.Fatalf("CopyWorktreeIncludes = %v, %v; want local/conf.txt", included, err)
	}
	if fi, err := os.Stat(filepath.Join(wt, "local")); err != nil || fi.Mode().Perm() != 0o555 {
		t.Fatalf("local/ mode = %v, %v; want the source's 0555", fi.Mode(), err)
	}
}
