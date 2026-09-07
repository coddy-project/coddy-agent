package permission

// Godog harness for features/background_permissions.feature: drives the
// permission dialog options and the session grant store directly, so the
// scenarios describe what the operator sees and what it buys them without
// involving a model or a real shell.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

type backgroundPermissionsState struct {
	state *session.State

	// command and options are staged by the "asks permission to run" step and
	// read by the steps that inspect the dialog or accept one of its choices.
	command string
	options []acp.PermissionOption
}

func (s *backgroundPermissionsState) reset() {
	s.state = &session.State{}
	s.command = ""
	s.options = nil
}

func (s *backgroundPermissionsState) sessionWithoutGrants() error {
	s.reset()
	if got := s.state.GetPermissionCommandGrants(); len(got) != 0 {
		return fmt.Errorf("fresh session already carries grants %v", got)
	}
	return nil
}

func (s *backgroundPermissionsState) argsJSON() string {
	args, err := json.Marshal(struct {
		Command string `json:"command"`
	}{Command: s.command})
	if err != nil {
		return `{}`
	}
	return string(args)
}

func (s *backgroundPermissionsState) asksPermission(command string) error {
	s.command = command
	s.options = Options("run_command", s.argsJSON())
	if len(s.options) == 0 {
		return fmt.Errorf("permission dialog offered no options at all")
	}
	return nil
}

func (s *backgroundPermissionsState) programOption() (acp.PermissionOption, bool) {
	for _, o := range s.options {
		if o.OptionID == OptionAllowAlwaysProgram {
			return o, true
		}
	}
	return acp.PermissionOption{}, false
}

func (s *backgroundPermissionsState) offersProgramOption(grant string) error {
	option, ok := s.programOption()
	if !ok {
		return fmt.Errorf("permission dialog offered no program-wide option for %q", s.command)
	}
	want := "Always allow " + grant
	if option.Name != want {
		return fmt.Errorf("program-wide option is named %q, want %q", option.Name, want)
	}
	return nil
}

func (s *backgroundPermissionsState) offersNoProgramOption() error {
	if option, ok := s.programOption(); ok {
		return fmt.Errorf("permission dialog offered %q for %q, want no program-wide option", option.Name, s.command)
	}
	return nil
}

func (s *backgroundPermissionsState) picksProgramOption() error {
	if _, ok := s.programOption(); !ok {
		return fmt.Errorf("there is no program-wide option to pick for %q", s.command)
	}
	RecordAllowAlways(s.state, "run_command", s.argsJSON(), "/repo", &acp.PermissionResult{OptionID: OptionAllowAlwaysProgram})
	return nil
}

func (s *backgroundPermissionsState) sessionGrants(grant string) error {
	grants := s.state.GetPermissionCommandGrants()
	if slices.Contains(grants, grant) {
		return nil
	}
	return fmt.Errorf("session grants %v do not include %q", grants, grant)
}

func (s *backgroundPermissionsState) allowed(command string) bool {
	return CommandAllowedWithSession(&tooling.Env{}, s.state.GetPermissionCommandGrants(), command)
}

func (s *backgroundPermissionsState) noLongerNeedsPermission(command string) error {
	if !s.allowed(command) {
		return fmt.Errorf("%q still needs permission under grants %v", command, s.state.GetPermissionCommandGrants())
	}
	return nil
}

func (s *backgroundPermissionsState) stillNeedsPermission(command string) error {
	if s.allowed(command) {
		return fmt.Errorf("%q runs without permission under grants %v", command, s.state.GetPermissionCommandGrants())
	}
	return nil
}

func initializeBackgroundPermissionsScenario(sc *godog.ScenarioContext) {
	s := &backgroundPermissionsState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})

	sc.Step(`^a session with no command grants$`, s.sessionWithoutGrants)
	sc.Step(`^the agent asks permission to run "([^"]*)"$`, s.asksPermission)
	sc.Step(`^the permission dialog offers to always allow "([^"]*)"$`, s.offersProgramOption)
	sc.Step(`^the permission dialog offers no program-wide option$`, s.offersNoProgramOption)
	sc.Step(`^the operator picks that program-wide option$`, s.picksProgramOption)
	sc.Step(`^the session grants "([^"]*)"$`, s.sessionGrants)
	sc.Step(`^running "([^"]*)" no longer needs permission$`, s.noLongerNeedsPermission)
	sc.Step(`^running "([^"]*)" still needs permission$`, s.stillNeedsPermission)
}

func TestBackgroundPermissionsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "background-permissions",
		ScenarioInitializer: initializeBackgroundPermissionsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/background_permissions.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("background permissions feature suite failed")
	}
}
