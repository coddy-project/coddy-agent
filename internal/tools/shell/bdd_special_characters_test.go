package shell

// Godog harness for features/shell_special_characters.feature: drives the
// run_command tool (executeRunCommandWithShell) against the real host shell and
// asserts that a command carrying shell special characters or non-ASCII text
// round-trips verbatim, and that a non-zero exit is still reported as a failure.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

type shellSpecialCharsState struct {
	shell platform.Shell

	// Text staged by the "prints the literal text" step, compared against the
	// tool output by the "contains that text verbatim" step.
	want string

	out string
	err error
}

func (s *shellSpecialCharsState) reset() {
	s.shell = platform.Shell{}
	s.want = ""
	s.out = ""
	s.err = nil
}

func (s *shellSpecialCharsState) hostShell() error {
	s.shell = platform.CurrentShell()
	if s.shell.Path == "" {
		return fmt.Errorf("no host shell detected")
	}
	return nil
}

// printCommand builds a command that writes text verbatim on the host shell.
// Both forms wrap the text in a single-quoted literal, where every other
// character (including the backticks and quotes this feature is about) is inert;
// only an embedded single quote needs escaping, and the two shells spell that
// escape differently.
func printCommand(kind platform.ShellKind, text string) (string, error) {
	switch kind {
	case platform.ShellPwsh, platform.ShellPowerShell:
		return "Write-Output '" + strings.ReplaceAll(text, "'", "''") + "'", nil
	case platform.ShellBash, platform.ShellSh:
		return "printf '%s\\n' '" + strings.ReplaceAll(text, "'", `'\''`) + "'", nil
	default:
		return "", fmt.Errorf("printing literal text is not supported for shell %q", kind)
	}
}

func (s *shellSpecialCharsState) runPrinting(doc *godog.DocString) error {
	command, err := printCommand(s.shell.Kind, doc.Content)
	if err != nil {
		return err
	}
	s.want = doc.Content
	return s.run(command)
}

func (s *shellSpecialCharsState) runExiting(code int) error {
	return s.run(fmt.Sprintf("exit %d", code))
}

func (s *shellSpecialCharsState) run(command string) error {
	args, err := json.Marshal(runCommandArgs{Command: command})
	if err != nil {
		return err
	}
	s.out, s.err = executeRunCommandWithShell(context.Background(), string(args), &tooling.Env{}, s.shell)
	return nil
}

func (s *shellSpecialCharsState) commandSucceeds() error {
	if s.err != nil {
		return fmt.Errorf("command returned error %v (output %q)", s.err, s.out)
	}
	if strings.Contains(s.out, "command failed:") {
		return fmt.Errorf("command reported failure: %q", s.out)
	}
	return nil
}

func (s *shellSpecialCharsState) commandFailed() error {
	if s.err != nil {
		return nil
	}
	if !strings.Contains(s.out, "command failed:") {
		return fmt.Errorf("command output does not report a failure: %q", s.out)
	}
	return nil
}

func (s *shellSpecialCharsState) failureReportsExitCode(code int) error {
	want := fmt.Sprintf("exit status %d", code)
	if !strings.Contains(s.out, want) {
		return fmt.Errorf("output %q does not report %q", s.out, want)
	}
	return nil
}

func (s *shellSpecialCharsState) outputContainsStagedText() error {
	if !strings.Contains(s.out, s.want) {
		return fmt.Errorf("output %q does not contain %q verbatim", s.out, s.want)
	}
	return nil
}

func initializeShellSpecialCharsScenario(sc *godog.ScenarioContext) {
	s := &shellSpecialCharsState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})

	sc.Step(`^the host shell detected by the agent$`, s.hostShell)
	sc.Step(`^I run a command that prints the literal text:$`, s.runPrinting)
	sc.Step(`^I run a command that exits with code (\d+)$`, s.runExiting)
	sc.Step(`^the command succeeds$`, s.commandSucceeds)
	sc.Step(`^the command is reported as failed$`, s.commandFailed)
	sc.Step(`^the failure reports exit code (\d+)$`, s.failureReportsExitCode)
	sc.Step(`^the output contains that text verbatim$`, s.outputContainsStagedText)
}

func TestShellSpecialCharactersFeature(t *testing.T) {
	if kind := platform.CurrentShell().Kind; kind == platform.ShellCmd {
		t.Skipf("cmd.exe quoting is out of scope: run_command uses PowerShell on Windows (detected %q)", kind)
	}
	suite := godog.TestSuite{
		Name:                "shell-special-characters",
		ScenarioInitializer: initializeShellSpecialCharsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/shell_special_characters.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("shell special characters feature suite failed")
	}
}
