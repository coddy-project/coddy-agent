package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
)

// gitWorkspace makes the rig's workspace a repository with a tracked pkg
// folder and one commit.
func gitWorkspace(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "pkg.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "pkg"},
		{"-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-q", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// writeStep is a child model step that writes file with content.
func writeStep(id, file, content string) scriptStep {
	args, _ := json.Marshal(map[string]string{"path": file, "content": content})
	return toolStep(llm.ToolCall{ID: id, Name: "write", InputJSON: string(args)})
}

func TestParseIsolation(t *testing.T) {
	for raw, want := range map[string]string{"": "", "none": "", " Worktree ": subagents.IsolationWorktree, "worktree": subagents.IsolationWorktree} {
		got, err := subagents.ParseIsolation(raw)
		if err != nil || got != want {
			t.Errorf("ParseIsolation(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	if _, err := subagents.ParseIsolation("container"); err == nil {
		t.Error("ParseIsolation(container) = nil error, want a refusal")
	}
	if _, err := subagents.Parse("x.md", []byte("---\nname: x\ndescription: d\nisolation: sandbox\n---\nbody\n")); err == nil {
		t.Error("a definition with isolation sandbox parsed, want a refusal")
	}
}

// A child that should work in a worktree of its own is refused where no
// worktree can be opened, before anything starts: no task, no limiter slot
// held.
func TestIsolatedSpawnOutsideAGitRepositoryIsRefused(t *testing.T) {
	rig := newSubagentRig(t, nil)
	rig.approvedDefinition("fixer", "isolation: worktree\n")
	before := subagentLimiter.InFlight()
	_, err := rig.parentAgent().spawnSubagent(context.Background(), spawnReq("fixer"))
	if err == nil || !strings.Contains(err.Error(), "isolation worktree") {
		t.Fatalf("isolated spawn outside git = %v, want a refusal naming isolation worktree", err)
	}
	if got := subagentLimiter.InFlight(); got != before {
		t.Fatalf("the refusal kept a limiter slot: %d in flight, want %d", got, before)
	}
	if n := len(rig.agentTasks()); n != 0 {
		t.Fatalf("the refusal started %d agent task(s)", n)
	}
}

// A parent in a subfolder of its checkout hands the child the same subfolder
// of the worktree.
func TestIsolatedChildStartsInTheSameFolderOfItsWorktree(t *testing.T) {
	rig := newSubagentRig(t, nil)
	gitWorkspace(t, rig.cwd)
	// From the user scope: the project folder of pkg has no definitions.
	rig.userDefinition("fixer", "isolation: worktree\n")
	rig.parent.SetCWD(filepath.Join(rig.cwd, "pkg"))
	rig.setChildProvider(func(*session.State) llm.Provider { return scripted(answerStep("REPORT: looked")) })
	mustSpawn(t, rig.parentAgent(), spawnReq("fixer"))
	task := rig.lastAgentTask()
	if task.Agent.Worktree == "" {
		t.Fatalf("task %+v names no worktree", task.Agent)
	}
	if want := filepath.Join(task.Agent.Worktree, "pkg"); !session.SameWorkspacePath(task.CWD, want) {
		t.Fatalf("child worked in %s, want %s", task.CWD, want)
	}
}

// A background spawn says at once where the child works.
func TestIsolatedBackgroundSpawnNamesTheWorktree(t *testing.T) {
	rig := newSubagentRig(t, nil)
	gitWorkspace(t, rig.cwd)
	rig.approvedDefinition("fixer", "isolation: worktree\n")
	rig.setChildProvider(func(*session.State) llm.Provider { return scripted(answerStep("REPORT: done")) })
	req := spawnReq("fixer")
	req.Background = true
	res := mustSpawn(t, rig.parentAgent(), req)
	task := rig.lastAgentTask()
	if !strings.Contains(res, "works in the worktree "+task.Agent.Worktree+" on branch "+task.Agent.Branch) {
		t.Fatalf("background result does not name the worktree %s:\n%s", task.Agent.Worktree, res)
	}
	rig.waitTask(task.ID)
}

// A resumed run goes on in the worktree its earlier run left work in.
func TestResumeOfAnIsolatedChildGoesOnInItsWorktree(t *testing.T) {
	rig := newSubagentRig(t, nil)
	gitWorkspace(t, rig.cwd)
	rig.approvedDefinition("fixer", "isolation: worktree\n")
	rig.setChildProvider(func(*session.State) llm.Provider {
		return scripted(writeStep("call_w", "fix.txt", "first\n"), answerStep("REPORT: wrote"), answerStep("REPORT: went on"))
	})
	first := parseSubagentEnvelope(t, mustSpawn(t, rig.parentAgent(), spawnReq("fixer")))
	kept := rig.lastAgentTask()
	if _, err := os.Stat(filepath.Join(kept.Agent.Worktree, "fix.txt")); err != nil {
		t.Fatalf("the first run's worktree lost its file: %v", err)
	}
	req := spawnReq("fixer")
	req.Resume = first.Task
	second := parseSubagentEnvelope(t, mustSpawn(t, rig.parentAgent(), req))
	if second.Session != first.Session || !strings.Contains(second.Body, "REPORT: went on") {
		t.Fatalf("resumed run = %+v, want the same child going on", second)
	}
	again := rig.lastAgentTask()
	if !session.SameWorkspacePath(again.Agent.Worktree, kept.Agent.Worktree) || again.Agent.Branch != kept.Agent.Branch ||
		!session.SameWorkspacePath(again.CWD, kept.CWD) {
		t.Fatalf("resumed run worked in %s (%s, %s), want the first run's %s (%s)", again.CWD, again.Agent.Worktree, again.Agent.Branch, kept.Agent.Worktree, kept.Agent.Branch)
	}
}

// A resumed run whose worktree went with the earlier run's end (it left
// nothing) gets the same worktree back, and loses it again when it leaves
// nothing either.
func TestResumeOfAnIsolatedChildWhoseWorktreeWentOpensItAgain(t *testing.T) {
	rig := newSubagentRig(t, nil)
	gitWorkspace(t, rig.cwd)
	rig.approvedDefinition("fixer", "isolation: worktree\n")
	rig.setChildProvider(func(*session.State) llm.Provider {
		return scripted(answerStep("REPORT: nothing"), answerStep("REPORT: still nothing"))
	})
	first := parseSubagentEnvelope(t, mustSpawn(t, rig.parentAgent(), spawnReq("fixer")))
	gone := rig.lastAgentTask()
	if _, err := os.Stat(gone.Agent.Worktree); !os.IsNotExist(err) {
		t.Fatalf("the first run's empty worktree stayed: %v", err)
	}
	req := spawnReq("fixer")
	req.Resume = first.Task
	res := mustSpawn(t, rig.parentAgent(), req)
	second := parseSubagentEnvelope(t, res)
	if second.Status != "succeeded" || !strings.Contains(second.Body, "REPORT: still nothing") {
		t.Fatalf("resumed run = %+v\n%s", second, res)
	}
	again := rig.lastAgentTask()
	if !session.SameWorkspacePath(again.Agent.Worktree, gone.Agent.Worktree) {
		t.Fatalf("resumed run worked in %s, want %s again", again.Agent.Worktree, gone.Agent.Worktree)
	}
	if _, err := os.Stat(again.Agent.Worktree); !os.IsNotExist(err) {
		t.Fatalf("the resumed run's empty worktree stayed: %v", err)
	}
	if !strings.Contains(res, "both are removed") {
		t.Fatalf("result does not say the worktree went:\n%s", res)
	}
}

// A run that worked in the parent's folder goes on there: asking for a
// worktree on a resume is refused rather than moving its transcript.
func TestResumeCannotMoveARunIntoAWorktree(t *testing.T) {
	rig := newSubagentRig(t, nil)
	gitWorkspace(t, rig.cwd)
	rig.approvedDefinition("worker", "")
	rig.setChildProvider(func(*session.State) llm.Provider { return scripted(answerStep("REPORT: done")) })
	first := parseSubagentEnvelope(t, mustSpawn(t, rig.parentAgent(), spawnReq("worker")))
	req := spawnReq("worker")
	req.Resume = first.Task
	req.Isolation = subagents.IsolationWorktree
	if _, err := rig.parentAgent().spawnSubagent(context.Background(), req); err == nil || !strings.Contains(err.Error(), "not in a worktree of its own") {
		t.Fatalf("resume into a worktree = %v, want a refusal", err)
	}
}
