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
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
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

// On a narrow line with every note up - the MCP count, the running tasks, the
// permission mode - the notes stay whole together and the path gives way.
func TestFooterKeepsEveryNoteWhenTheLineIsNarrow(t *testing.T) {
	cwd := "/var/folders/36/tjdph2t965j8snz9_vkdnw0r0000gn/T/coddy-cli-bdd-1789734719342464000/work"
	f := newFooter(newTheme("dark"), cwd)
	f.title = "Session manager walkthrough"
	f.SetMCP(1, 3, true)
	f.SetRunningTasks(2)
	f.permission = "bypass"
	line := tui.StripTerminalSequences(f.Render(80)[0])
	for _, note := range []string{"MCP 1/3", "2 tasks running (/tasks)", "bypass"} {
		if !strings.Contains(line, note) {
			t.Fatalf("the note %q fell off the footer:\n%s", note, line)
		}
	}
	if got := tui.VisibleWidth(line); got > 80 {
		t.Fatalf("footer line is %d cells wide, want at most 80", got)
	}
	if !strings.Contains(line, "/var/folders") {
		t.Fatalf("the path vanished instead of giving way:\n%s", line)
	}
}

// On a line with room for the notes and not for any of the path, the path
// goes and the notes stay.
func TestFooterDropsThePathBeforeANote(t *testing.T) {
	f := newFooter(newTheme("dark"), "/home/operator/projects/coddy")
	f.SetMCP(1, 3, true)
	f.SetRunningTasks(2)
	f.permission = "bypass"
	line := tui.StripTerminalSequences(f.Render(48)[0])
	for _, note := range []string{"MCP 1/3", "2 tasks running (/tasks)", "bypass"} {
		if !strings.Contains(line, note) {
			t.Fatalf("the note %q fell off the footer:\n%s", note, line)
		}
	}
	if got := tui.VisibleWidth(line); got > 48 {
		t.Fatalf("footer line is %d cells wide, want at most 48", got)
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

// The footer is two lines: where the session works on the left and what it
// runs on on the right. The model, with its reasoning level when it has one,
// closes the first line; the account's plan and quota close the second,
// under the model; the context left of it reads what the window holds and
// nothing more.
func TestFooterLaysTheModelAndTheUsageOutOnTheRight(t *testing.T) {
	f := newFooter(newTheme("dark"), "/work")
	f.gitBranch = "main"
	f.now = func() time.Time { return usageNow }
	f.SetModel("neuraldeep/qwen3.8-27b", "high")
	f.AddTokens(50700, 146)
	f.SetContext(9.7, 262144)
	f.SetUsage(usageFixtureUpdate())
	lines := f.Render(120)
	if len(lines) != 2 {
		t.Fatalf("the footer is two lines with the usage under the model, got %d:\n%s", len(lines), plainLines(lines))
	}
	first, second := plain(lines[0]), plain(lines[1])
	if !strings.HasPrefix(first, "/work (main)") || !strings.HasSuffix(first, "(neuraldeep) qwen3.8-27b • high") {
		t.Fatalf("the first line is the folder left and the model with its level right:\n%q", first)
	}
	if !strings.HasPrefix(second, "↑50.7k ↓146 9.7%/262.1k") || !strings.HasSuffix(strings.TrimRight(second, " "), "wallet -1 229 ₽") {
		t.Fatalf("the second line is the context left and the usage right:\n%q", second)
	}
	if strings.Contains(second, "(auto)") || strings.Contains(second, "qwen") {
		t.Fatalf("the context reads the window alone and the model is not repeated:\n%q", second)
	}
	for i, line := range []string{first, second} {
		if got := tui.VisibleWidth(line); got != 120 {
			t.Fatalf("line %d is %d cells wide, want the right side at the edge of 120", i+1, got)
		}
	}
}

// A model without reasoning levels names no level, and without a usage
// source the second line holds the context alone.
func TestFooterWithoutALevelOrUsage(t *testing.T) {
	f := newFooter(newTheme("dark"), "/work")
	f.SetModel("stub/model-one", "")
	f.SetContext(1.5, 100000)
	lines := f.Render(100)
	if len(lines) != 2 {
		t.Fatalf("footer = %q", plainLines(lines))
	}
	if first := strings.TrimRight(plain(lines[0]), " "); !strings.HasSuffix(first, "(stub) model-one") {
		t.Fatalf("the model closes the first line with no level after it: %q", first)
	}
	if second := strings.TrimRight(plain(lines[1]), " "); second != "1.5%/100k" {
		t.Fatalf("second line = %q, want the context alone", second)
	}
}

// On a narrow line the notes of the first line stay whole and the model
// shares it with them: the path gives way first, then the model is cut.
func TestFooterKeepsTheNotesBesideTheModelOnANarrowLine(t *testing.T) {
	f := newFooter(newTheme("dark"), "/home/operator/projects/coddy")
	f.SetModel("neuraldeep/qwen3.8-27b-noreason", "medium")
	f.SetMCP(1, 3, true)
	f.SetRunningTasks(2)
	f.permission = "bypass"
	for _, width := range []int{120, 80, 60} {
		line := plain(f.Render(width)[0])
		for _, note := range []string{"MCP 1/3", "2 tasks running (/tasks)", "bypass"} {
			if !strings.Contains(line, note) {
				t.Fatalf("at %d cells the note %q fell off:\n%q", width, note, line)
			}
		}
		if got := tui.VisibleWidth(line); got > width {
			t.Fatalf("at %d cells the line is %d wide", width, got)
		}
		if !strings.Contains(line, "(neuraldeep") {
			t.Fatalf("at %d cells the model is gone instead of cut:\n%q", width, line)
		}
	}
	if line := plain(f.Render(120)[0]); !strings.HasSuffix(line, "(neuraldeep) qwen3.8-27b-noreason • medium") {
		t.Fatalf("with room the model is whole: %q", line)
	}
}

// Before the first turn a session on a provider without a usage source has
// nothing for the second line; the overrides line then follows the first one
// with no blank row between them.
func TestFooterLeavesNoBlankRowAboveTheOverrides(t *testing.T) {
	f := newFooter(newTheme("dark"), "/work")
	f.SetModel("stub/coddy-demo", "")
	f.SetSettings("bypass", []acp.TurnOverride{{Setting: "model", Value: "stub/coddy-mini", TurnsLeft: 1}})
	lines := f.Render(100)
	if len(lines) != 2 || !strings.Contains(plain(lines[1]), "next turn: model stub/coddy-mini") {
		t.Fatalf("footer = %q", plainLines(lines))
	}
	// Without overrides the empty second line stays, so the footer keeps its
	// height when the first turn fills it.
	f.SetSettings("bypass", nil)
	if lines := f.Render(100); len(lines) != 2 || strings.TrimSpace(plain(lines[1])) != "" {
		t.Fatalf("footer = %q", plainLines(lines))
	}
}
