package main

// Godog harness for features/cli_help.feature: `coddy help` and
// `coddy help <command>` run in process, what they print captured.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

type helpCLIState struct {
	out string
	err error
	// failed names the commands that answered with no usage of their own.
	failed []string
}

// runs runs one `coddy help ...` command and captures what it printed on
// either stream.
func (s *helpCLIState) runs(command string) error {
	fields := strings.Fields(command)
	if len(fields) < 2 || fields[0] != "coddy" || fields[1] != "help" {
		return fmt.Errorf("not a coddy help command: %q", command)
	}
	s.err = nil
	out, err := captureOutput(func() { s.err = runHelp(fields[2:]) })
	if err != nil {
		return err
	}
	s.out = out
	return nil
}

// runsForEveryCommand asks `coddy help <command>` of every command the usage
// text lists, the set the packaging rule keeps the man page and the
// completions to, and keeps the names that answered with nothing of their own.
func (s *helpCLIState) runsForEveryCommand() error {
	s.failed = nil
	for _, cmd := range topLevelCommands {
		out, err := captureOutput(func() { s.err = runHelp([]string{cmd}) })
		if err != nil {
			return err
		}
		if s.err != nil || !strings.Contains(out, cmd) {
			s.failed = append(s.failed, cmd)
		}
	}
	s.err = nil
	return nil
}

func (s *helpCLIState) commandSucceeds() error {
	if s.err != nil {
		return fmt.Errorf("coddy help failed: %v", s.err)
	}
	return nil
}

func (s *helpCLIState) outputMentions(text string) error {
	if !strings.Contains(s.out, text) {
		return fmt.Errorf("the output does not mention %q:\n%.400s", text, s.out)
	}
	return nil
}

func (s *helpCLIState) everyCommandAnswered() error {
	if len(s.failed) > 0 {
		return fmt.Errorf("coddy help answered with no usage of its own for: %s", strings.Join(s.failed, ", "))
	}
	return nil
}

func initializeHelpCLIScenario(sc *godog.ScenarioContext) {
	s := &helpCLIState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.out = ""
		s.err = nil
		s.failed = nil
		return ctx, nil
	})
	sc.Step(`^the operator runs "([^"]*)"$`, s.runs)
	sc.Step(`^the operator runs "coddy help" for every top-level command$`, s.runsForEveryCommand)
	sc.Step(`^the command succeeds$`, s.commandSucceeds)
	sc.Step(`^the output mentions "([^"]*)"$`, s.outputMentions)
	sc.Step(`^every command answered with its usage$`, s.everyCommandAnswered)
}

func TestCLIHelpFeature(t *testing.T) {
	withTempHome(t)
	suite := godog.TestSuite{
		Name:                "cli-help",
		ScenarioInitializer: initializeHelpCLIScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/cli_help.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("cli help feature suite failed")
	}
}
