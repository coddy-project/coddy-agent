//go:build cli

package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
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
