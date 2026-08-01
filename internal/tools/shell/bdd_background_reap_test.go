package shell

// Godog harness for features/background_reap.feature.
//
// The "earlier coddy run" is a second, real pool over the same session bundle:
// it starts a genuine detached process and is then simply abandoned, which is
// what a killed process leaves behind. The fresh pool reads the record its
// predecessor persisted, exactly as a restarted coddy does, so the scenarios
// exercise the real liveness probe against a real pid rather than a stub.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

const backgroundReapSessionID = "bdd-background-reap"

// taskMetaPath is the record a task mirrors into the session bundle. The layout
// is part of the documented on-disk contract (docs/background-tasks.md), which
// is why a harness outside internal/bgtask may read it.
func taskMetaPath(sessionDir, taskID string) string {
	return filepath.Join(sessionDir, "background", taskID, "meta.json")
}

type backgroundReapState struct {
	shell      platform.Shell
	sessionDir string

	// earlier is the pool of the run that was killed. It is deliberately never
	// drained: its process has to outlive it for there to be anything to reap.
	earlier *tooling.Env

	env     *tooling.Env
	taskID  string
	pid     int
	started time.Time

	listing string
	reaping string
}

func (s *backgroundReapState) reset(t *testing.T) {
	s.shell = platform.CurrentShell()
	s.sessionDir = t.TempDir()
	s.earlier = nil
	s.env = nil
	s.taskID = ""
	s.pid = 0
	s.started = time.Time{}
	s.listing = ""
	s.reaping = ""
}

// newEnv builds the tool environment of one coddy process over the session
// bundle. Every call is a separate pool, which is what "a fresh coddy" means.
func (s *backgroundReapState) newEnv() *tooling.Env {
	return &tooling.Env{
		SessionID:         backgroundReapSessionID,
		SessionDir:        s.sessionDir,
		BackgroundEnabled: true,
		Background:        bgtask.NewWithRunner(bgtask.Config{}, bgtask.NewCommandRunner()),
	}
}

// cleanup kills whatever is still on the machine. A scenario that fails midway
// must not leave a ten-minute sleep behind.
func (s *backgroundReapState) cleanup() {
	if s.earlier != nil {
		s.earlier.Background.StopSession(backgroundReapSessionID)
	}
	if s.env != nil {
		s.env.Background.StopSession(backgroundReapSessionID)
	}
	if s.pid > 0 {
		_ = platform.TerminateProcessGroupByPID(s.pid, time.Second)
	}
}

func (s *backgroundReapState) emptyPool() error {
	s.env = s.newEnv()
	if got := s.env.Background.List(backgroundReapSessionID); len(got) != 0 {
		return fmt.Errorf("fresh pool already holds %d tasks", len(got))
	}
	return nil
}

// startTask runs a long sleep in the background through run_command and records
// what the pool made of it.
func (s *backgroundReapState) startTask(env *tooling.Env) error {
	command, err := sleepCommand(s.shell.Kind, 600)
	if err != nil {
		return err
	}
	args, err := json.Marshal(runCommandArgs{Command: command, Background: true})
	if err != nil {
		return err
	}
	if _, err := executeRunCommandWithShell(context.Background(), string(args), env, s.shell); err != nil {
		return fmt.Errorf("run_command returned %v", err)
	}

	tasks := env.Background.List(backgroundReapSessionID)
	if len(tasks) == 0 {
		return fmt.Errorf("run_command started nothing")
	}
	task := tasks[len(tasks)-1]
	if task.PID <= 0 {
		return fmt.Errorf("task %s carries no pid, so nothing could ever reach it", task.ID)
	}
	s.taskID = task.ID
	s.pid = task.PID
	s.started = task.StartedAt
	return nil
}

func (s *backgroundReapState) earlierRunLeftATaskRunning() error {
	s.earlier = s.newEnv()
	if err := s.startTask(s.earlier); err != nil {
		return err
	}
	// The record has to still claim the task was running, which is what a run
	// that was killed rather than drained leaves on disk.
	raw, err := os.ReadFile(taskMetaPath(s.sessionDir, s.taskID))
	if err != nil {
		return fmt.Errorf("the earlier run persisted no record: %w", err)
	}
	var snap bgtask.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return err
	}
	if snap.Status != bgtask.StatusRunning {
		return fmt.Errorf("the persisted record says %q, want running", snap.Status)
	}
	return nil
}

func (s *backgroundReapState) thisCoddyStartedATask() error {
	return s.startTask(s.env)
}

// freshCoddyTakesOver abandons the earlier pool without stopping it, the way a
// killed process abandons its children, and reads the bundle with a new one.
func (s *backgroundReapState) freshCoddyTakesOver() error {
	s.env = s.newEnv()
	if got := s.env.Background.List(backgroundReapSessionID); len(got) != 0 {
		return fmt.Errorf("a fresh pool cannot already own %d tasks", len(got))
	}
	return nil
}

func (s *backgroundReapState) listTasks() error {
	out, err := BackgroundListTool().Execute(context.Background(), "{}", s.env)
	if err != nil {
		return err
	}
	s.listing = out
	return nil
}

const stillAliveMarker = "still alive from an earlier run"

func (s *backgroundReapState) listingMarksTaskAsLeftover() error {
	if err := s.listTasks(); err != nil {
		return err
	}
	if !strings.Contains(s.listing, s.taskID) {
		return fmt.Errorf("listing %q does not name task %q", s.listing, s.taskID)
	}
	if !strings.Contains(s.listing, stillAliveMarker) {
		return fmt.Errorf("listing %q does not mark task %q as %q", s.listing, s.taskID, stillAliveMarker)
	}
	return nil
}

func (s *backgroundReapState) listingDoesNotOfferTaskForReaping() error {
	if !strings.Contains(s.listing, s.taskID) {
		return fmt.Errorf("listing %q does not name task %q at all", s.listing, s.taskID)
	}
	if strings.Contains(s.listing, stillAliveMarker) {
		return fmt.Errorf("listing %q offers a task this pool is supervising for reaping", s.listing)
	}
	return nil
}

func (s *backgroundReapState) reap() error {
	out, err := BackgroundReapTool().Execute(context.Background(), "{}", s.env)
	if err != nil {
		return err
	}
	s.reaping = out
	return nil
}

func (s *backgroundReapState) reapingReportsTask() error {
	if !strings.Contains(s.reaping, s.taskID) {
		return fmt.Errorf("reaping answered %q, which does not name task %q", s.reaping, s.taskID)
	}
	return nil
}

func (s *backgroundReapState) reapingKilledNothing() error {
	const want = "No leftover background processes"
	if !strings.Contains(s.reaping, want) {
		return fmt.Errorf("reaping answered %q, want %q", s.reaping, want)
	}
	return nil
}

func (s *backgroundReapState) leftoverProcessIsGone() error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !platform.ProcessGroupAlive(s.pid, s.started) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("the process group led by pid %d is still alive after reaping", s.pid)
}

func (s *backgroundReapState) taskIsStillRunning() error {
	snap, err := s.env.Background.Get(backgroundReapSessionID, s.taskID)
	if err != nil {
		return err
	}
	if snap.Status != bgtask.StatusRunning {
		return fmt.Errorf("task %s is %q, want running", s.taskID, snap.Status)
	}
	return nil
}

func initializeBackgroundReapScenario(t *testing.T, sc *godog.ScenarioContext) {
	s := &backgroundReapState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset(t)
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^an earlier coddy run left a background task running$`, s.earlierRunLeftATaskRunning)
	sc.Step(`^a session with an empty background task pool$`, s.emptyPool)
	sc.Step(`^this coddy started a background task of its own$`, s.thisCoddyStartedATask)
	sc.Step(`^a fresh coddy takes over the same session bundle$`, s.freshCoddyTakesOver)
	sc.Step(`^I list the background tasks$`, s.listTasks)
	sc.Step(`^the task listing marks that task as still alive from an earlier run$`, s.listingMarksTaskAsLeftover)
	sc.Step(`^the listing does not offer that task for reaping$`, s.listingDoesNotOfferTaskForReaping)
	sc.Step(`^I reap the leftover background processes$`, s.reap)
	sc.Step(`^the reaping reports that task$`, s.reapingReportsTask)
	sc.Step(`^the reaping reports that there was nothing to kill$`, s.reapingKilledNothing)
	sc.Step(`^the leftover process is gone$`, s.leftoverProcessIsGone)
	sc.Step(`^that task is still running$`, s.taskIsStillRunning)
}

func TestBackgroundReapFeature(t *testing.T) {
	if kind := platform.CurrentShell().Kind; kind == platform.ShellCmd {
		t.Skipf("background tasks use PowerShell on Windows (detected %q)", kind)
	}
	suite := godog.TestSuite{
		Name: "background-reap",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			initializeBackgroundReapScenario(t, sc)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/background_reap.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("background reap feature suite failed")
	}
}
