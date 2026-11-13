package permission

// Godog harness for features/command_allowlist.feature: asks the permission
// gate's own predicate whether a command needs a prompt under the operator's
// allowlist, with the shell pinned so the scenarios read the same on every
// host.

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

var quotedEntry = regexp.MustCompile(`"([^"]*)"`)

type commandAllowlistState struct {
	env *tooling.Env
}

func (s *commandAllowlistState) posixShell() error {
	s.env.Shell = platform.ShellBash
	return nil
}

func (s *commandAllowlistState) allowlistHolds(entries string) error {
	for _, m := range quotedEntry.FindAllStringSubmatch(entries, -1) {
		s.env.CommandAllowlist = append(s.env.CommandAllowlist, m[1])
	}
	if len(s.env.CommandAllowlist) == 0 {
		return fmt.Errorf("no quoted entry in %q", entries)
	}
	return nil
}

func (s *commandAllowlistState) needsNoPermission(command string) error {
	if !CommandAllowedWithSession(s.env, nil, command) {
		return fmt.Errorf("%q needs permission under the allowlist %q", command, s.env.CommandAllowlist)
	}
	return nil
}

func (s *commandAllowlistState) stillNeedsPermission(command string) error {
	if CommandAllowedWithSession(s.env, nil, command) {
		return fmt.Errorf("%q runs without permission under the allowlist %q", command, s.env.CommandAllowlist)
	}
	return nil
}

func initializeCommandAllowlistScenario(sc *godog.ScenarioContext) {
	s := &commandAllowlistState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.env = &tooling.Env{}
		return ctx, nil
	})

	sc.Step(`^commands run in a POSIX shell$`, s.posixShell)
	sc.Step(`^the operator's command allowlist holds (.+)$`, s.allowlistHolds)
	sc.Step(`^running "([^"]*)" needs no permission$`, s.needsNoPermission)
	sc.Step(`^running "([^"]*)" still needs permission$`, s.stillNeedsPermission)
}

func TestCommandAllowlistFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "command-allowlist",
		ScenarioInitializer: initializeCommandAllowlistScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/command_allowlist.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("command allowlist feature suite failed")
	}
}
