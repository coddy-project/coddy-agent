package gitws

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAndSwitchWithoutGit(t *testing.T) {
	dir := initRepo(t)
	head := mustGit(t, dir, "rev-parse", "HEAD")
	hideGitBinary(t)
	if err := CreateAndSwitch(dir, "feature/builtin"); err != nil {
		t.Fatal(err)
	}
	if got := mustGit(t, dir, "branch", "--show-current"); got != "feature/builtin" {
		t.Fatalf("branch = %s", got)
	}
	if got := mustGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD = %s", got)
	}
	if err := CreateAndSwitch(dir, "main"); err == nil {
		t.Fatal("replaced an existing branch")
	}
	if _, err := History(context.Background(), dir, 0, 100); !errors.Is(err, ErrNeedsGitBinary) {
		t.Fatalf("history error = %v", err)
	}
}

func TestCreateAndSwitchWithoutGitKeepsTrackedEdits(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "tracked.txt")
	mustGit(t, dir, "-c", "user.email=coddy@test", "-c", "user.name=Coddy", "commit", "-m", "track file")
	head := mustGit(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(path, []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hideGitBinary(t)
	if err := CreateAndSwitch(dir, "feature/with-edit"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "edited\n" {
		t.Fatalf("tracked edit = %q", content)
	}
	if got := mustGit(t, dir, "branch", "--show-current"); got != "feature/with-edit" {
		t.Fatalf("branch = %q", got)
	}
	if got := mustGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD = %s, want %s", got, head)
	}
}

func TestHistoryEmptyAndDetachedHEAD(t *testing.T) {
	empty := t.TempDir()
	mustGit(t, empty, "init", "-b", "main")
	page, err := History(context.Background(), empty, 0, 100)
	if err != nil || len(page.Commits) != 0 {
		t.Fatalf("empty history = %+v %v", page, err)
	}
	dir := initRepo(t)
	mustGit(t, dir, "checkout", "--detach")
	mustGit(t, dir, "-c", "user.email=coddy@test", "-c", "user.name=Coddy", "commit", "--allow-empty", "-m", "Detached commit")
	mustGit(t, dir, "-c", "user.email=coddy@test", "-c", "user.name=Coddy", "tag", "-a", "release", "-m", "Annotated release")
	page, err = History(context.Background(), dir, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if page.Commits[0].Subject != "Detached commit" || len(page.Commits[0].Refs) != 2 {
		t.Fatalf("detached = %+v", page.Commits[0])
	}
}

func TestCreateAndSwitchFromCurrentHEAD(t *testing.T) {
	dir := initRepo(t)
	head := mustGit(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "draft.txt"), []byte("draft"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CreateAndSwitch(dir, "feature/new"); err != nil {
		t.Fatal(err)
	}
	if got := mustGit(t, dir, "branch", "--show-current"); got != "feature/new" {
		t.Fatalf("branch = %q", got)
	}
	if got := mustGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD changed: %s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "draft.txt")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"feature/new", "--force", "bad name", "@{-1}", "HEAD", ""} {
		if err := CreateAndSwitch(dir, name); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
}

func TestHistoryIncludesMergeParentsAndRefs(t *testing.T) {
	dir := initRepo(t)
	mustGit(t, dir, "checkout", "feature/login")
	mustGit(t, dir, "-c", "user.email=coddy@test", "-c", "user.name=Coddy", "commit", "--allow-empty", "-m", "Feature | with punctuation")
	feature := mustGit(t, dir, "rev-parse", "HEAD")
	mustGit(t, dir, "checkout", "main")
	mustGit(t, dir, "-c", "user.email=coddy@test", "-c", "user.name=Coddy", "merge", "--no-ff", "feature/login", "-m", "Merge feature")
	mustGit(t, dir, "tag", "v1")
	page, err := History(context.Background(), dir, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Commits) != 2 || !page.HasMore {
		t.Fatalf("page = %+v", page)
	}
	if len(page.Commits[0].Parents) != 2 {
		t.Fatalf("parents = %v", page.Commits[0].Parents)
	}
	if page.Commits[1].Hash != feature {
		t.Fatalf("topological order = %+v", page.Commits)
	}
	if len(page.Commits[0].Refs) < 3 {
		t.Fatalf("refs = %v", page.Commits[0].Refs)
	}
	next, err := History(context.Background(), dir, 2, 2)
	if err != nil || len(next.Commits) != 1 || next.HasMore {
		t.Fatalf("next = %+v, %v", next, err)
	}
}

func TestHistoryLeavesOutStashAndNotes(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "draft.txt"), []byte("draft"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "draft.txt")
	mustGit(t, dir, "-c", "user.email=coddy@test", "-c", "user.name=Coddy", "stash")
	mustGit(t, dir, "-c", "user.email=coddy@test", "-c", "user.name=Coddy", "notes", "add", "-m", "a note", "HEAD")
	page, err := History(context.Background(), dir, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Commits) != 1 {
		t.Fatalf("commits = %+v", page.Commits)
	}
	for _, row := range page.Commits {
		if strings.Contains(row.Subject, "WIP on") || strings.Contains(row.Subject, "index on") || strings.Contains(row.Subject, "Notes added") {
			t.Fatalf("service commit in the graph: %+v", row)
		}
	}
	if got := mustGit(t, dir, "stash", "list"); got == "" {
		t.Fatal("the stash disappeared")
	}
	if got := mustGit(t, dir, "notes", "list"); got == "" {
		t.Fatal("the notes disappeared")
	}
}
