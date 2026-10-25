//go:build unix

package gitws

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		return 0
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
		dir, want := repoWithChanges(t, n)
		var changes []WorkChange
		runs[n] = countGit(t, func() {
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
	}
	if runs[25] != runs[5] {
		t.Fatalf("git runs grew with the files changed: %d for 5, %d for 25", runs[5], runs[25])
	}
	if runs[5] > 6 {
		t.Fatalf("git runs = %d, want at most 6", runs[5])
	}
}
