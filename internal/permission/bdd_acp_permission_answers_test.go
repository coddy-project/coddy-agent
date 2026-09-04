package permission

// Godog harness for features/acp_permission_answers.feature: feeds the raw JSON
// an ACP client puts on the wire through the same decode and decision path the
// agent loop uses, so the scenarios describe what the operator's click buys
// them without involving a model, an editor, or a real shell.

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

type acpPermissionAnswersState struct {
	state   *session.State
	command string
	result  *acp.PermissionResult
}

func (s *acpPermissionAnswersState) reset() {
	s.state = &session.State{}
	s.command = ""
	s.result = nil
}

func (s *acpPermissionAnswersState) sessionWithoutGrants() error {
	s.reset()
	if got := s.state.GetPermissionCommandGrants(); len(got) != 0 {
		return fmt.Errorf("fresh session already carries grants %v", got)
	}
	return nil
}

func (s *acpPermissionAnswersState) argsJSON() string {
	args, err := json.Marshal(struct {
		Command string `json:"command"`
	}{Command: s.command})
	if err != nil {
		return `{}`
	}
	return string(args)
}

func (s *acpPermissionAnswersState) asksPermission(command string) error {
	s.command = command
	if len(Options("run_command", s.argsJSON())) == 0 {
		return fmt.Errorf("permission dialog offered no options at all")
	}
	return nil
}

// editorAnswers decodes the response body an ACP editor sends, which nests the
// outcome in its own object, and applies it the way the agent loop does.
func (s *acpPermissionAnswersState) editorAnswers(optionID string) error {
	offered := Options("run_command", s.argsJSON())
	if !slices.ContainsFunc(offered, func(o acp.PermissionOption) bool { return o.OptionID == optionID }) {
		return fmt.Errorf("the dialog for %q offers no option %q", s.command, optionID)
	}
	body := fmt.Sprintf(`{"outcome":{"outcome":"selected","optionId":%q}}`, optionID)
	var result acp.PermissionResult
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		return fmt.Errorf("decoding the editor's answer %s: %w", body, err)
	}
	s.result = &result
	if Approved(&result) {
		RecordAllowAlways(s.state, "run_command", s.argsJSON(), "/repo", &result)
	}
	return nil
}

func (s *acpPermissionAnswersState) answerLetsCommandRun() error {
	if s.result == nil {
		return fmt.Errorf("no answer was decoded")
	}
	if !Approved(s.result) {
		return fmt.Errorf("answer %+v was read as a refusal, want an approval", *s.result)
	}
	return nil
}

func (s *acpPermissionAnswersState) answerRefusesCommand() error {
	if s.result == nil {
		return fmt.Errorf("no answer was decoded")
	}
	if Approved(s.result) {
		return fmt.Errorf("answer %+v was read as an approval, want a refusal", *s.result)
	}
	return nil
}

func (s *acpPermissionAnswersState) sessionGrants(grant string) error {
	grants := s.state.GetPermissionCommandGrants()
	if slices.Contains(grants, grant) {
		return nil
	}
	return fmt.Errorf("session grants %v do not include %q", grants, grant)
}

func (s *acpPermissionAnswersState) allowed(command string) bool {
	return CommandAllowedWithSession(&tooling.Env{}, s.state.GetPermissionCommandGrants(), command)
}

func (s *acpPermissionAnswersState) noLongerNeedsPermission(command string) error {
	if !s.allowed(command) {
		return fmt.Errorf("%q still needs permission under grants %v", command, s.state.GetPermissionCommandGrants())
	}
	return nil
}

func (s *acpPermissionAnswersState) stillNeedsPermission(command string) error {
	if s.allowed(command) {
		return fmt.Errorf("%q runs without permission under grants %v", command, s.state.GetPermissionCommandGrants())
	}
	return nil
}

func initializeACPPermissionAnswersScenario(sc *godog.ScenarioContext) {
	s := &acpPermissionAnswersState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})

	sc.Step(`^a session with no command grants$`, s.sessionWithoutGrants)
	sc.Step(`^the agent asks permission to run "([^"]*)"$`, s.asksPermission)
	sc.Step(`^the editor answers "([^"]*)" the way the protocol nests its outcome$`, s.editorAnswers)
	sc.Step(`^the answer lets the command run$`, s.answerLetsCommandRun)
	sc.Step(`^the answer refuses the command$`, s.answerRefusesCommand)
	sc.Step(`^the session grants "([^"]*)"$`, s.sessionGrants)
	sc.Step(`^running "([^"]*)" no longer needs permission$`, s.noLongerNeedsPermission)
	sc.Step(`^running "([^"]*)" still needs permission$`, s.stillNeedsPermission)
}

func TestACPPermissionAnswersFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "acp-permission-answers",
		ScenarioInitializer: initializeACPPermissionAnswersScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/acp_permission_answers.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("acp permission answers feature suite failed")
	}
}
