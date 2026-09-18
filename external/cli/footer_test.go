//go:build cli

package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
)

// TestDetectGitBranchGivesUpOnAHungGit pins the bound behind the footer's
// branch label: a git that never answers yields an empty label within the
// timeout instead of holding the console before its first frame.
func TestDetectGitBranchGivesUpOnAHungGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hung git stand-in is a shell script")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	prev := gitBranchTimeout
	gitBranchTimeout = 200 * time.Millisecond
	t.Cleanup(func() { gitBranchTimeout = prev })

	started := time.Now()
	if got := detectGitBranch(t.TempDir()); got != "" {
		t.Fatalf("branch = %q, want empty for a git that never answered", got)
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Fatalf("detectGitBranch waited %v for a hung git", took)
	}
}

// TestDetectGitBranchReadsTheBranch keeps the label itself honest: a real
// repository on the checked-out branch names it.
func TestDetectGitBranchReadsTheBranch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in git is a shell script")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho feature/x\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got := detectGitBranch(t.TempDir()); got != "feature/x" {
		t.Fatalf("branch = %q, want feature/x", got)
	}
}

func TestFooterNamesRunningTasksAndTheCommandThatListsThem(t *testing.T) {
	f := newFooter(newTheme("dark"), "/work")
	if got := strings.Join(f.Render(120), "\n"); strings.Contains(got, "running") {
		t.Fatalf("an idle session names running tasks:\n%s", got)
	}
	f.SetRunningTasks(1)
	if got := f.Render(120)[0]; !strings.Contains(got, "1 task running (/tasks)") {
		t.Fatalf("footer = %q", got)
	}
	f.SetRunningTasks(3)
	if got := f.Render(120)[0]; !strings.Contains(got, "3 tasks running (/tasks)") {
		t.Fatalf("footer = %q", got)
	}
}

// A long working directory - a macOS temp folder, a deep monorepo path - must not push
// the running tasks off the line: the path gives way, the count and the command that
// lists the tasks stay.
func TestFooterKeepsTheRunningTasksWhenThePathIsLong(t *testing.T) {
	cwd := "/var/folders/36/tjdph2t965j8snz9_vkdnw0r0000gn/T/coddy-cli-bdd-1789734719342464000/work"
	f := newFooter(newTheme("dark"), cwd)
	f.title = "Session manager walkthrough"
	f.SetRunningTasks(1)
	line := f.Render(100)[0]
	if !strings.Contains(line, "1 task running (/tasks)") {
		t.Fatalf("the running tasks fell off the footer:\n%s", line)
	}
	if got := tui.VisibleWidth(line); got > 100 {
		t.Fatalf("footer line is %d cells wide, want at most 100", got)
	}
	if !strings.Contains(line, "/var/folders") {
		t.Fatalf("the path vanished instead of giving way:\n%s", line)
	}
}
