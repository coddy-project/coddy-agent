package agent

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
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

// parkedHandle is a background task that runs until it is stopped.
type parkedHandle struct{ done chan struct{} }

func (h *parkedHandle) Wait() (int, error) { <-h.done; return 0, nil }
func (h *parkedHandle) Stop(time.Duration) error {
	select {
	case <-h.done:
	default:
		close(h.done)
	}
	return nil
}
func (h *parkedHandle) PID() int                    { return 0 }
func (h *parkedHandle) ProcessStartedAt() time.Time { return time.Time{} }

// openedChildWorktree opens a child's worktree in the rig's repository the way
// a spawn does, and a run that owns it.
func openedChildWorktree(t *testing.T, rig *subagentRig) (*childWorktree, *subagentRun, string) {
	t.Helper()
	childID := session.NewSessionID()
	w, _, _, err := openChildWorktree(rig.cwd, "fixer", childID, nil)
	if err != nil {
		t.Fatal(err)
	}
	return w, &subagentRun{taskID: "bg_child_itself", worktree: w}, childID
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A background task of another session still at work in the child's
// worktree keeps it, empty as it is: its folder is not removed under it.
func TestSettleKeepsAWorktreeABackgroundTaskRunsIn(t *testing.T) {
	rig := newSubagentRig(t, nil)
	gitWorkspace(t, rig.cwd)
	w, run, _ := openedChildWorktree(t, rig)
	pool := bgtask.New(bgtask.Config{})
	t.Cleanup(func() { pool.StopSession("sess_watcher") })
	if _, err := pool.Launch(bgtask.Spec{SessionID: "sess_watcher", Kind: bgtask.KindServer, Label: "tests", CWD: filepath.Join(w.Path, "pkg")},
		func(string, io.Writer) (bgtask.Handle, error) { return &parkedHandle{done: make(chan struct{})}, nil }); err != nil {
		t.Fatal(err)
	}
	settleChildWorktree(rig.mgr, pool, run)
	if w.Removed || !strings.Contains(w.Kept, "background task") {
		t.Fatalf("settled = %+v, want the worktree kept for the running task", w)
	}
	if _, err := os.Stat(w.Path); err != nil {
		t.Fatalf("the worktree went under the task: %v", err)
	}
}

// A child's commits keep its worktree even when another ref holds them all
// (a push, a branch of its own): the parent is not told it left nothing, and
// a resume finds the commits where they were.
func TestSettleKeepsAWorktreeWhoseChildCommitted(t *testing.T) {
	rig := newSubagentRig(t, nil)
	gitWorkspace(t, rig.cwd)
	w, run, _ := openedChildWorktree(t, rig)
	if err := os.WriteFile(filepath.Join(w.Path, "fix.txt"), []byte("fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, w.Path, "add", "fix.txt")
	gitIn(t, w.Path, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-q", "-m", "fix")
	gitIn(t, rig.cwd, "branch", "pushed-copy", w.Branch) // holds the commit, as a remote branch would
	settleChildWorktree(rig.mgr, bgtask.New(bgtask.Config{}), run)
	if w.Removed || !strings.Contains(w.Kept, "committed") {
		t.Fatalf("settled = %+v, want the worktree kept for the child's commit", w)
	}
}

// A child that moved its worktree to another branch is reported on that
// branch, and a resume goes on in the same worktree instead of failing to
// check the first branch out over it.
func TestAChildThatSwitchedBranchesIsResumedWhereItIs(t *testing.T) {
	rig := newSubagentRig(t, nil)
	gitWorkspace(t, rig.cwd)
	w, run, childID := openedChildWorktree(t, rig)
	first := w.Branch
	gitIn(t, w.Path, "switch", "-q", "-c", "fix/actual")
	if err := os.WriteFile(filepath.Join(w.Path, "fix.txt"), []byte("fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	settleChildWorktree(rig.mgr, bgtask.New(bgtask.Config{}), run)
	if w.Removed || w.Branch != "fix/actual" || !strings.Contains(run.worktree.reportLine(), "fix/actual") {
		t.Fatalf("settled = %+v, want it kept and reported on fix/actual", w)
	}
	again, folder, _, err := openChildWorktree(rig.cwd, "fixer", childID, &resumeTarget{worktree: w.Path, cwd: w.Path})
	if err != nil {
		t.Fatalf("resume of a child on another branch = %v", err)
	}
	if !session.SameWorkspacePath(again.Path, w.Path) || again.Branch != "fix/actual" || again.Assigned != first || !session.SameWorkspacePath(folder, w.Path) {
		t.Fatalf("resumed in %s on %s, assigned %s (folder %s), want %s on fix/actual with %s assigned", again.Path, again.Branch, again.Assigned, folder, w.Path, first)
	}
	// Even with nothing left in it, a worktree on another branch is not the
	// run's to remove, and that branch is not the run's to delete.
	if err := os.Remove(filepath.Join(w.Path, "fix.txt")); err != nil {
		t.Fatal(err)
	}
	settleChildWorktree(rig.mgr, bgtask.New(bgtask.Config{}), &subagentRun{taskID: "bg_resumed", worktree: again})
	if again.Removed || !strings.Contains(again.Kept, "moved it from branch "+first+" to fix/actual") {
		t.Fatalf("settled = %+v, want it kept on the branch the child moved it to", again)
	}
}

// A child that committed and then went back to the start commit, detached or
// on another branch, keeps its worktree, and no branch is deleted that the
// run was not given.
func TestSettleKeepsAWorktreeTheChildLeftOffItsBranch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		leave func(t *testing.T, dir, start string)
		want  string
	}{
		{"detached at the start commit", func(t *testing.T, dir, start string) {
			gitIn(t, dir, "checkout", "-q", "--detach", start)
		}, "detached HEAD"},
		{"an existing branch at the start commit", func(t *testing.T, dir, start string) {
			gitIn(t, dir, "checkout", "-q", "develop")
		}, "to develop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newSubagentRig(t, nil)
			gitWorkspace(t, rig.cwd)
			gitIn(t, rig.cwd, "branch", "develop")
			w, run, _ := openedChildWorktree(t, rig)
			if err := os.WriteFile(filepath.Join(w.Path, "fix.txt"), []byte("fix\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitIn(t, w.Path, "add", "fix.txt")
			gitIn(t, w.Path, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-q", "-m", "fix")
			committed := gitIn(t, w.Path, "rev-parse", "HEAD")
			tc.leave(t, w.Path, w.Start)
			settleChildWorktree(rig.mgr, bgtask.New(bgtask.Config{}), run)
			if w.Removed || !strings.Contains(w.Kept, tc.want) {
				t.Fatalf("settled = %+v, want it kept (%s)", w, tc.want)
			}
			if got := gitIn(t, rig.cwd, "rev-parse", w.Assigned); got != committed {
				t.Fatalf("%s = %s, want the child's commit %s", w.Assigned, got, committed)
			}
			if got := gitIn(t, rig.cwd, "branch", "--list", "develop"); got == "" {
				t.Fatal("develop was deleted")
			}
		})
	}
}

// A resume that made the worktree again and was then refused by the pool
// leaves no empty worktree behind.
func TestARefusedResumeLeavesNoWorktreeBehind(t *testing.T) {
	rig := newSubagentRig(t, nil)
	gitWorkspace(t, rig.cwd)
	rig.approvedDefinition("fixer", "isolation: worktree\n")
	rig.setChildProvider(func(*session.State) llm.Provider { return scripted(answerStep("REPORT: nothing")) })
	first := parseSubagentEnvelope(t, mustSpawn(t, rig.parentAgent(), spawnReq("fixer")))
	gone := rig.lastAgentTask().Agent.Worktree
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Fatalf("the first run's empty worktree stayed: %v", err)
	}
	pool := bgtask.Default()
	pool.SetDraining(true)
	t.Cleanup(func() { pool.SetDraining(false) })
	req := spawnReq("fixer")
	req.Resume = first.Task
	if _, err := rig.parentAgent().spawnSubagent(context.Background(), req); err == nil {
		t.Fatal("a resume into a draining pool started")
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Fatalf("the refused resume left its worktree %s: %v", gone, err)
	}
	if out := gitIn(t, rig.cwd, "branch", "--list", "subagent/*"); out != "" {
		t.Fatalf("the refused resume left a branch: %q", out)
	}
}
