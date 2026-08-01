package shell

// Godog harness for features/background_tasks.feature: drives run_command and
// the background task tools against a real pool on the host shell, so the
// scenarios assert what the agent actually observes rather than what a stub
// would report.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

type backgroundTasksState struct {
	pool  *bgtask.Pool
	env   *tooling.Env
	shell platform.Shell

	// startCall records how long run_command itself took, which is what the
	// "does not wait" step is about.
	startCall time.Duration

	taskID  string
	listing string
	output  string
}

func (s *backgroundTasksState) reset() {
	s.pool = bgtask.NewWithRunner(bgtask.Config{}, bgtask.NewCommandRunner())
	s.shell = platform.CurrentShell()
	s.env = &tooling.Env{
		SessionID:         "bdd-background",
		BackgroundEnabled: true,
		Background:        s.pool,
	}
	s.startCall = 0
	s.taskID = ""
	s.listing = ""
	s.output = ""
}

func (s *backgroundTasksState) cleanup() {
	if s.pool != nil {
		s.pool.StopSession("bdd-background")
	}
}

func (s *backgroundTasksState) emptyPool() error {
	s.reset()
	if got := s.pool.List(s.env.SessionID); len(got) != 0 {
		return fmt.Errorf("fresh pool already holds %d tasks", len(got))
	}
	return nil
}

// sleepCommand and printCommand keep the scenarios shell-neutral: the feature
// talks about sleeping and printing, not about POSIX syntax.
func sleepCommand(kind platform.ShellKind, seconds int) (string, error) {
	switch kind {
	case platform.ShellPwsh, platform.ShellPowerShell:
		return fmt.Sprintf("Start-Sleep -Seconds %d", seconds), nil
	case platform.ShellBash, platform.ShellSh:
		return fmt.Sprintf("sleep %d", seconds), nil
	default:
		return "", fmt.Errorf("sleeping is not supported for shell %q", kind)
	}
}

func (s *backgroundTasksState) start(command string, expectedSeconds, timeoutSeconds int) error {
	args, err := json.Marshal(runCommandArgs{
		Command:         command,
		Background:      true,
		ExpectedSeconds: expectedSeconds,
		TimeoutSeconds:  timeoutSeconds,
	})
	if err != nil {
		return err
	}

	began := time.Now()
	out, err := executeRunCommandWithShell(context.Background(), string(args), s.env, s.shell)
	s.startCall = time.Since(began)
	if err != nil {
		return fmt.Errorf("run_command returned %v", err)
	}
	s.output = out

	tasks := s.pool.List(s.env.SessionID)
	if len(tasks) == 0 {
		return fmt.Errorf("run_command reported %q but the pool holds no task", out)
	}
	s.taskID = tasks[len(tasks)-1].ID
	return nil
}

func (s *backgroundTasksState) startSleeping(seconds int) error {
	command, err := sleepCommand(s.shell.Kind, seconds)
	if err != nil {
		return err
	}
	return s.start(command, 0, 0)
}

func (s *backgroundTasksState) startSleepingWithEstimate(seconds, estimate int) error {
	command, err := sleepCommand(s.shell.Kind, seconds)
	if err != nil {
		return err
	}
	return s.start(command, estimate, 0)
}

func (s *backgroundTasksState) startSleepingWithTimeout(seconds, timeout int) error {
	command, err := sleepCommand(s.shell.Kind, seconds)
	if err != nil {
		return err
	}
	return s.start(command, 0, timeout)
}

func (s *backgroundTasksState) startPrinting(text string) error {
	command, err := printCommand(s.shell.Kind, text)
	if err != nil {
		return err
	}
	return s.start(command, 0, 0)
}

func (s *backgroundTasksState) reportsTaskID() error {
	if s.taskID == "" {
		return fmt.Errorf("no task was registered")
	}
	if !strings.Contains(s.output, s.taskID) {
		return fmt.Errorf("run_command answered %q, which does not name task %q", s.output, s.taskID)
	}
	return nil
}

func (s *backgroundTasksState) didNotWait() error {
	const budget = 2 * time.Second
	if s.startCall > budget {
		return fmt.Errorf("run_command blocked for %s, want less than %s", s.startCall, budget)
	}
	return nil
}

func (s *backgroundTasksState) snapshot() (bgtask.Snapshot, error) {
	return s.pool.Get(s.env.SessionID, s.taskID)
}

func (s *backgroundTasksState) statusIs(want bgtask.Status) error {
	snap, err := s.snapshot()
	if err != nil {
		return err
	}
	if snap.Status != want {
		return fmt.Errorf("task %s is %q, want %q", s.taskID, snap.Status, want)
	}
	return nil
}

func (s *backgroundTasksState) taskIsRunning() error {
	return s.statusIs(bgtask.StatusRunning)
}

func (s *backgroundTasksState) taskSucceeded() error {
	return s.statusIs(bgtask.StatusSucceeded)
}

func (s *backgroundTasksState) taskIsStopped() error {
	return s.statusIs(bgtask.StatusStopped)
}

func (s *backgroundTasksState) taskTimedOut() error {
	return s.statusIs(bgtask.StatusTimedOut)
}

func (s *backgroundTasksState) taskNoLongerRunning() error {
	snap, err := s.snapshot()
	if err != nil {
		return err
	}
	if !snap.Status.Finished() {
		return fmt.Errorf("task %s is still %q", s.taskID, snap.Status)
	}
	return nil
}

func (s *backgroundTasksState) listTasks() error {
	tool := BackgroundListTool()
	out, err := tool.Execute(context.Background(), "{}", s.env)
	if err != nil {
		return err
	}
	s.listing = out
	return nil
}

func (s *backgroundTasksState) listingNamesTask() error {
	if !strings.Contains(s.listing, s.taskID) {
		return fmt.Errorf("listing %q does not name task %q", s.listing, s.taskID)
	}
	return nil
}

func (s *backgroundTasksState) listingReportsRunning() error {
	if !strings.Contains(s.listing, string(bgtask.StatusRunning)) {
		return fmt.Errorf("listing %q does not report the task as running", s.listing)
	}
	return nil
}

func (s *backgroundTasksState) listingReportsEstimate(seconds int) error {
	want := "estimated " + humanSeconds(seconds)
	if !strings.Contains(s.listing, want) {
		return fmt.Errorf("listing %q does not report %q", s.listing, want)
	}
	return nil
}

func (s *backgroundTasksState) listingReportsElapsed() error {
	if !strings.Contains(s.listing, "elapsed ") {
		return fmt.Errorf("listing %q does not report an elapsed time", s.listing)
	}
	return nil
}

func (s *backgroundTasksState) waitForTask() error {
	tool := BackgroundWaitTool()
	args, err := json.Marshal(map[string]interface{}{"task_id": s.taskID, "timeout_seconds": 30})
	if err != nil {
		return err
	}
	out, err := tool.Execute(context.Background(), string(args), s.env)
	if err != nil {
		return err
	}
	s.output = out
	return nil
}

func (s *backgroundTasksState) stopTask() error {
	tool := BackgroundStopTool()
	args, err := json.Marshal(map[string]interface{}{"task_id": s.taskID})
	if err != nil {
		return err
	}
	out, err := tool.Execute(context.Background(), string(args), s.env)
	if err != nil {
		return err
	}
	s.output = out
	return nil
}

func (s *backgroundTasksState) outputContains(text string) error {
	tool := BackgroundOutputTool()
	args, err := json.Marshal(map[string]interface{}{"task_id": s.taskID})
	if err != nil {
		return err
	}
	out, err := tool.Execute(context.Background(), string(args), s.env)
	if err != nil {
		return err
	}
	if !strings.Contains(out, text) {
		return fmt.Errorf("task output %q does not contain %q", out, text)
	}
	return nil
}

func initializeBackgroundTasksScenario(sc *godog.ScenarioContext) {
	s := &backgroundTasksState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^a session with an empty background task pool$`, s.emptyPool)
	sc.Step(`^I run a command in the background that sleeps for (\d+) seconds$`, s.startSleeping)
	sc.Step(`^I run a command in the background that sleeps for (\d+) seconds with an estimate of (\d+) seconds$`, s.startSleepingWithEstimate)
	sc.Step(`^I run a command in the background that sleeps for (\d+) seconds with a timeout of (\d+) second$`, s.startSleepingWithTimeout)
	sc.Step(`^I run a command in the background that prints "([^"]*)"$`, s.startPrinting)
	sc.Step(`^the tool reports a background task id$`, s.reportsTaskID)
	sc.Step(`^the tool does not wait for the command to finish$`, s.didNotWait)
	sc.Step(`^the task is running$`, s.taskIsRunning)
	sc.Step(`^I list the background tasks$`, s.listTasks)
	sc.Step(`^the listing names that task$`, s.listingNamesTask)
	sc.Step(`^the listing reports the task as running$`, s.listingReportsRunning)
	sc.Step(`^the listing reports an estimate of (\d+) seconds$`, s.listingReportsEstimate)
	sc.Step(`^the listing reports the elapsed time$`, s.listingReportsElapsed)
	sc.Step(`^I wait for that background task to finish$`, s.waitForTask)
	sc.Step(`^I stop that background task$`, s.stopTask)
	sc.Step(`^the task succeeded$`, s.taskSucceeded)
	sc.Step(`^the task is stopped$`, s.taskIsStopped)
	sc.Step(`^the task timed out$`, s.taskTimedOut)
	sc.Step(`^the task is no longer running$`, s.taskNoLongerRunning)
	sc.Step(`^the task output contains "([^"]*)"$`, s.outputContains)
}

func TestBackgroundTasksFeature(t *testing.T) {
	if kind := platform.CurrentShell().Kind; kind == platform.ShellCmd {
		t.Skipf("background tasks use PowerShell on Windows (detected %q)", kind)
	}
	suite := godog.TestSuite{
		Name:                "background-tasks",
		ScenarioInitializer: initializeBackgroundTasksScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/background_tasks.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("background tasks feature suite failed")
	}
}
