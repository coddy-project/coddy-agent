//go:build unix

package gitws

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// countGit puts a git on PATH that records each run and hands it to the real
// one, and returns how many runs fn caused.
func countGit(t *testing.T, fn func()) int {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "runs.log")
	script := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> %q\nexec %q \"$@\"\n", log, gitBin)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	fn()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}

func repoWithChanges(t *testing.T, modified int) (dir string, want map[string][2]string) {
	t.Helper()
	files := map[string]string{"sub/keep.txt": "same\n", "sub/gone.txt": "bye\n", "top.txt": "top\n"}
	for i := 0; i < modified; i++ {
		files[fmt.Sprintf("sub/f%02d.txt", i)] = fmt.Sprintf("line %d\n", i)
	}
	dir = commitRepo(t, files)
	want = map[string][2]string{}
	for i := 0; i < modified; i++ {
		name := fmt.Sprintf("f%02d.txt", i)
		writeFile(t, dir, "sub/"+name, fmt.Sprintf("line %d\nadded\n", i))
		want[name] = [2]string{fmt.Sprintf("line %d\n", i), fmt.Sprintf("line %d\nadded\n", i)}
	}
	if err := os.Remove(filepath.Join(dir, "sub", "gone.txt")); err != nil {
		t.Fatal(err)
	}
	want["gone.txt"] = [2]string{"bye\n", ""}
	writeFile(t, dir, "sub/new.txt", "fresh\n")
	want["new.txt"] = [2]string{"", "fresh\n"}
	writeFile(t, dir, "top.txt", "outside the session folder\n")
	return filepath.Join(dir, "sub"), want
}

// The scope of a session folder costs a fixed number of git runs, however
// many files changed, and still reads both sides of each from the right
// place: HEAD, relative to the folder, and the disk.
func TestWorktreeChangesRunsGitAFixedNumberOfTimes(t *testing.T) {
	if gitBin == "" {
		t.Skip("git binary not available")
	}
	runs := map[int]int{}
	for _, n := range []int{5, 25} {
		runs[n] = worktreeChangesGitRuns(t, n)
		t.Logf("%d modified files: %d git runs", n, runs[n])
	}
	if runs[25] != runs[5] {
		t.Fatalf("git runs grew with the files changed: %d for 5, %d for 25", runs[5], runs[25])
	}
	if runs[5] > 6 {
		t.Fatalf("git runs = %d, want at most 6", runs[5])
	}
}

func worktreeChangesGitRuns(t *testing.T, n int) int {
	t.Helper()
	dir, want := repoWithChanges(t, n)
	var changes []WorkChange
	runs := countGit(t, func() {
		var err error
		changes, _, err = WorktreeChanges(dir)
		if err != nil {
			t.Fatal(err)
		}
	})
	if len(changes) != len(want) {
		t.Fatalf("%d files changed: got %d changes, want %d", n, len(changes), len(want))
	}
	for _, c := range changes {
		w, ok := want[c.Path]
		if !ok {
			t.Fatalf("unexpected change %q", c.Path)
		}
		if string(c.Before) != w[0] || string(c.After) != w[1] {
			t.Fatalf("%s: before %q after %q, want %q / %q", c.Path, c.Before, c.After, w[0], w[1])
		}
	}
	return runs
}

func TestWorkingCopyGitProcessesFeature(t *testing.T) {
	if gitBin == "" {
		t.Skip("git binary not available")
	}
	var small, large int
	suite := godog.TestSuite{
		Name: "working-copy-git-processes",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^I read a session subfolder with (\d+) modified files$`, func(n int) {
				small = worktreeChangesGitRuns(t, n)
			})
			sc.Step(`^I read another session subfolder with (\d+) modified files$`, func(n int) {
				large = worktreeChangesGitRuns(t, n)
			})
			sc.Step(`^both reads use the same number of Git processes, at most (\d+)$`, func(limit int) error {
				if small == 0 || large != small || large > limit {
					return fmt.Errorf("Git processes: %d and %d, want equal and at most %d", small, large, limit)
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/working_copy_git_processes.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("working copy Git process feature failed")
	}
}
