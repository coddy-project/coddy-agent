package agent

import (
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// heldLaunch starts an agent-kind task that stays running until the test ends.
func heldLaunch(t *testing.T) bgtask.LaunchFunc {
	t.Helper()
	handle := &subagentHandle{cancel: func() {}, done: make(chan struct{})}
	t.Cleanup(func() { close(handle.done) })
	return func(string, io.Writer) (bgtask.Handle, error) { return handle, nil }
}

func turnContextAgent(t *testing.T, sessionID string) *Agent {
	t.Helper()
	cwd := t.TempDir()
	st := &session.State{ID: sessionID, CWD: cwd, Mode: session.ModeAgent}
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	return NewAgent(cfg, st, nil, nil)
}

// The model starts a task, keeps talking, and several steps later has to remember
// that the task exists. The turn context says what still runs on every request, so
// it does not take a background_list call to find out.
func TestTurnContextListsTheRunningBackgroundTasksOfTheSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the command below is a POSIX shell line")
	}
	const sessionID = "sess_turn_ctx_tasks"
	a := turnContextAgent(t, sessionID)
	pool := bgtask.Default()
	t.Cleanup(func() {
		pool.StopSession(sessionID)
		pool.ReleaseSession(sessionID)
	})
	sys := a.buildSystemPromptParts("agent", nil, nil, "", nil)

	if block := a.buildTurnContext(sys); strings.Contains(block, "## Background tasks") {
		t.Fatalf("a session with nothing running names background tasks:\n%s", block)
	}

	running, err := pool.Start(bgtask.Spec{SessionID: sessionID, Command: "sleep 30", CWD: t.TempDir(), ExpectedSeconds: 60})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	// The runtime's own errand is not the model's work.
	if _, err := pool.Launch(bgtask.Spec{SessionID: sessionID, Kind: bgtask.KindAgent, Label: "memory: recall",
		Agent: &bgtask.AgentInfo{Name: "memory", System: true}}, heldLaunch(t)); err != nil {
		t.Fatalf("Launch(): %v", err)
	}
	// Another session's task is not this session's business.
	other, err := pool.Start(bgtask.Spec{SessionID: sessionID + "_other", Command: "sleep 30", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("Start(other): %v", err)
	}
	t.Cleanup(func() {
		pool.StopSession(other.SessionID)
		pool.ReleaseSession(other.SessionID)
	})

	block := a.buildTurnContext(sys)
	if !strings.Contains(block, "## Background tasks") {
		t.Fatalf("the turn context does not say what runs:\n%s", block)
	}
	line := running.ID + " [running] sleep 30 (elapsed "
	if !strings.Contains(block, line) || !strings.Contains(block, "estimated 1m") {
		t.Fatalf("the running task is not listed the way background_list lists it (%q):\n%s", line, block)
	}
	if strings.Contains(block, "memory: recall") {
		t.Fatalf("a system task reached the model:\n%s", block)
	}
	if strings.Count(block, "[running]") != 1 {
		t.Fatalf("the block lists tasks that are not this session's own:\n%s", block)
	}
	// The section stays out of the frozen system prompt: it moves on every step.
	if strings.Contains(sys.Content, "## Background tasks") {
		t.Fatal("running tasks reached the cached system prompt")
	}

	if _, err := pool.Stop(sessionID, running.ID); err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	if block := a.buildTurnContext(sys); strings.Contains(block, "[running]") {
		t.Fatalf("a stopped task is still listed as running:\n%s", block)
	}
}

func TestTurnContextNamesNoTasksWhenBackgroundRunsAreSwitchedOff(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the command below is a POSIX shell line")
	}
	const sessionID = "sess_turn_ctx_tasks_off"
	a := turnContextAgent(t, sessionID)
	off := false
	a.cfg.Tools.Background.Enabled = &off
	pool := bgtask.Default()
	t.Cleanup(func() {
		pool.StopSession(sessionID)
		pool.ReleaseSession(sessionID)
	})
	if _, err := pool.Start(bgtask.Spec{SessionID: sessionID, Command: "sleep 30", CWD: t.TempDir()}); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	// With the tools gone the model has nothing to read or stop a task with.
	sys := a.buildSystemPromptParts("agent", nil, nil, "", nil)
	if block := a.buildTurnContext(sys); strings.Contains(block, "## Background tasks") {
		t.Fatalf("background tasks are named with tools.background.enable false:\n%s", block)
	}
}
