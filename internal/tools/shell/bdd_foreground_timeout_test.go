package shell

// Godog harness for features/foreground_timeout_adoption.feature: runs real
// commands on the host shell through run_command and checks what happens to a
// command that is still working when its foreground timeout elapses. The
// scenarios use real processes on purpose - the bug being specified is about
// inherited pipes and process groups, which a stub runner cannot reproduce.

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

const foregroundTimeoutSessionID = "bdd-foreground-timeout"

// lastingSeconds is how long the commands in this feature keep running. It only
// has to outlast the scenario; cleanup stops whatever is left.
const lastingSeconds = 60

type foregroundTimeoutState struct {
	pool  *bgtask.Pool
	env   *tooling.Env
	shell platform.Shell

	answer string
	// call records how long run_command itself took, which is the whole point
	// of the scenarios that assert an answer arrives at all.
	call time.Duration

	taskID string
	// marker is the text the command printed before the timeout, staged by the
	// step that ran it and read back by the ordering assertion.
	marker string
}

func (s *foregroundTimeoutState) reset() {
	s.pool = bgtask.NewWithRunner(bgtask.Config{}, bgtask.NewCommandRunner())
	s.shell = platform.CurrentShell()
	s.env = &tooling.Env{
		SessionID:         foregroundTimeoutSessionID,
		BackgroundEnabled: true,
		Background:        s.pool,
	}
	s.answer = ""
	s.call = 0
	s.taskID = ""
	s.marker = ""
}

func (s *foregroundTimeoutState) cleanup() {
	if s.pool != nil {
		s.pool.StopSession(foregroundTimeoutSessionID)
	}
}

func (s *foregroundTimeoutState) emptyPool() error {
	s.reset()
	if got := s.pool.List(s.env.SessionID); len(got) != 0 {
		return fmt.Errorf("fresh pool already holds %d tasks", len(got))
	}
	return nil
}

// lastingChildCommand builds a command whose shell spawns a separate process
// that outlives a kill aimed only at the shell while still holding the shell's
// stdout handle. That is the shape of `npm run serve`, and the shape that used
// to wedge cmd.Wait forever.
//
// The POSIX form needs the trailing `; true`: without it the outer shell execs
// the inner one, no grandchild exists, and the scenario would pass even against
// the broken code.
func lastingChildCommand(sh platform.Shell, seconds int) (string, error) {
	switch sh.Kind {
	case platform.ShellPwsh, platform.ShellPowerShell:
		return fmt.Sprintf("& '%s' -NoProfile -Command \"Start-Sleep -Seconds %d\"", sh.Path, seconds), nil
	case platform.ShellBash, platform.ShellSh:
		return fmt.Sprintf("'%s' -c 'sleep %d'; true", sh.Path, seconds), nil
	default:
		return "", fmt.Errorf("spawning a lasting child is not supported for shell %q", sh.Kind)
	}
}

func (s *foregroundTimeoutState) run(command string, timeoutSeconds int) error {
	args, err := json.Marshal(runCommandArgs{Command: command, TimeoutSeconds: timeoutSeconds})
	if err != nil {
		return err
	}

	began := time.Now()
	out, execErr := executeRunCommandWithShell(context.Background(), string(args), s.env, s.shell)
	s.call = time.Since(began)
	if execErr != nil {
		return fmt.Errorf("run_command returned %v", execErr)
	}
	s.answer = out

	if tasks := s.pool.List(s.env.SessionID); len(tasks) > 0 {
		s.taskID = tasks[len(tasks)-1].ID
	}
	return nil
}

func (s *foregroundTimeoutState) runPastTimeout(timeoutSeconds int) error {
	command, err := sleepCommand(s.shell.Kind, lastingSeconds)
	if err != nil {
		return err
	}
	return s.run(command, timeoutSeconds)
}

func (s *foregroundTimeoutState) runPrintingPastTimeout(text string, timeoutSeconds int) error {
	printing, err := printCommand(s.shell.Kind, text)
	if err != nil {
		return err
	}
	sleeping, err := sleepCommand(s.shell.Kind, lastingSeconds)
	if err != nil {
		return err
	}
	s.marker = text
	return s.run(printing+"; "+sleeping, timeoutSeconds)
}

// complainCommand writes to the real stderr handle without any shell
// redirection, which is how a dev server reports a busy port. The tool captures
// stdout and stderr into the same sink, so the model reads it without ever
// needing 2>&1 - and on PowerShell that operator would mangle the message.
func complainCommand(kind platform.ShellKind, text string) (string, error) {
	switch kind {
	case platform.ShellPwsh, platform.ShellPowerShell:
		return "[Console]::Error.WriteLine('" + strings.ReplaceAll(text, "'", "''") + "')", nil
	case platform.ShellBash, platform.ShellSh:
		return "printf '%s\\n' '" + strings.ReplaceAll(text, "'", `'\''`) + "' >&2", nil
	default:
		return "", fmt.Errorf("writing to the error stream is not supported for shell %q", kind)
	}
}

func (s *foregroundTimeoutState) runComplainingPastTimeout(text string, timeoutSeconds int) error {
	complaining, err := complainCommand(s.shell.Kind, text)
	if err != nil {
		return err
	}
	sleeping, err := sleepCommand(s.shell.Kind, lastingSeconds)
	if err != nil {
		return err
	}
	s.marker = text
	return s.run(complaining+"; "+sleeping, timeoutSeconds)
}

func (s *foregroundTimeoutState) runSpawningLastingChild(timeoutSeconds int) error {
	command, err := lastingChildCommand(s.shell, lastingSeconds)
	if err != nil {
		return err
	}
	return s.run(command, timeoutSeconds)
}

func (s *foregroundTimeoutState) runPrinting(text string) error {
	command, err := printCommand(s.shell.Kind, text)
	if err != nil {
		return err
	}
	s.marker = text
	return s.run(command, 30)
}

func (s *foregroundTimeoutState) answersWithin(seconds int) error {
	budget := time.Duration(seconds) * time.Second
	if s.call > budget {
		return fmt.Errorf("run_command blocked for %s, want less than %s", s.call, budget)
	}
	return nil
}

func (s *foregroundTimeoutState) answerNamesNewTask() error {
	if s.taskID == "" {
		return fmt.Errorf("no task was registered; run_command answered %q", s.answer)
	}
	if !strings.Contains(s.answer, s.taskID) {
		return fmt.Errorf("run_command answered %q, which does not name task %q", s.answer, s.taskID)
	}
	return nil
}

func (s *foregroundTimeoutState) answerContains(text string) error {
	if !strings.Contains(s.answer, text) {
		return fmt.Errorf("run_command answered %q, which does not contain %q", s.answer, text)
	}
	return nil
}

func (s *foregroundTimeoutState) answerNamesTaskFirst() error {
	if err := s.answerNamesNewTask(); err != nil {
		return err
	}
	task := strings.Index(s.answer, s.taskID)
	output := strings.Index(s.answer, s.marker)
	if output < 0 {
		return fmt.Errorf("run_command answered %q, which does not show %q", s.answer, s.marker)
	}
	if task > output {
		return fmt.Errorf("run_command answered %q: the output comes before the task id, so truncation would drop the id", s.answer)
	}
	return nil
}

func (s *foregroundTimeoutState) snapshot() (bgtask.Snapshot, error) {
	return s.pool.Get(s.env.SessionID, s.taskID)
}

func (s *foregroundTimeoutState) adoptedTaskIsRunning() error {
	snap, err := s.snapshot()
	if err != nil {
		return err
	}
	if snap.Status != bgtask.StatusRunning {
		return fmt.Errorf("task %s is %q, want %q", s.taskID, snap.Status, bgtask.StatusRunning)
	}
	return nil
}

func (s *foregroundTimeoutState) stopAdoptedTask() error {
	args, err := json.Marshal(map[string]interface{}{"task_id": s.taskID})
	if err != nil {
		return err
	}
	_, err = BackgroundStopTool().Execute(context.Background(), string(args), s.env)
	return err
}

func (s *foregroundTimeoutState) adoptedTaskIsStopped() error {
	snap, err := s.snapshot()
	if err != nil {
		return err
	}
	if snap.Status != bgtask.StatusStopped {
		return fmt.Errorf("task %s is %q, want %q", s.taskID, snap.Status, bgtask.StatusStopped)
	}
	return nil
}

func (s *foregroundTimeoutState) adoptedTaskNoLongerRunning() error {
	snap, err := s.snapshot()
	if err != nil {
		return err
	}
	if !snap.Status.Finished() {
		return fmt.Errorf("task %s is still %q", s.taskID, snap.Status)
	}
	return nil
}

func (s *foregroundTimeoutState) noTaskCreated() error {
	if got := s.pool.List(s.env.SessionID); len(got) != 0 {
		return fmt.Errorf("pool holds %d tasks, want none", len(got))
	}
	return nil
}

func initializeForegroundTimeoutScenario(sc *godog.ScenarioContext) {
	s := &foregroundTimeoutState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^a session with an empty background task pool$`, s.emptyPool)
	sc.Step(`^I run a command in the foreground that keeps running past its (\d+) second timeout$`, s.runPastTimeout)
	sc.Step(`^I run a command in the foreground that prints "([^"]*)" and keeps running past its (\d+) second timeout$`, s.runPrintingPastTimeout)
	sc.Step(`^I run a command in the foreground that complains "([^"]*)" on its error stream and keeps running past its (\d+) second timeout$`, s.runComplainingPastTimeout)
	sc.Step(`^I run a command in the foreground that spawns a lasting child and keeps running past its (\d+) second timeout$`, s.runSpawningLastingChild)
	sc.Step(`^I run a command in the foreground that prints "([^"]*)"$`, s.runPrinting)
	sc.Step(`^the tool answers within (\d+) seconds$`, s.answersWithin)
	sc.Step(`^the answer names a new background task$`, s.answerNamesNewTask)
	sc.Step(`^that background task is running$`, s.adoptedTaskIsRunning)
	sc.Step(`^the answer contains "([^"]*)"$`, s.answerContains)
	sc.Step(`^the answer names the background task before it shows that output$`, s.answerNamesTaskFirst)
	sc.Step(`^I stop the adopted background task$`, s.stopAdoptedTask)
	sc.Step(`^the adopted task is stopped$`, s.adoptedTaskIsStopped)
	sc.Step(`^the adopted task is no longer running$`, s.adoptedTaskNoLongerRunning)
	sc.Step(`^no background task was created$`, s.noTaskCreated)
}

func TestForegroundTimeoutAdoptionFeature(t *testing.T) {
	if kind := platform.CurrentShell().Kind; kind == platform.ShellCmd {
		t.Skipf("run_command uses PowerShell on Windows (detected %q)", kind)
	}
	suite := godog.TestSuite{
		Name:                "foreground-timeout-adoption",
		ScenarioInitializer: initializeForegroundTimeoutScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/foreground_timeout_adoption.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("foreground timeout adoption feature suite failed")
	}
}
