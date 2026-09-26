//go:build cli

package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// footerPermission is the permission mode the footer names: the segment after
// the folder, empty while the session asks (the footer names only a mode that
// asks less).
func (s *cliTUIState) footerPermission() string {
	for _, line := range strings.Split(s.screenText(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, s.cwd) {
			continue
		}
		if i := strings.LastIndex(line, " • "); i >= 0 {
			return strings.TrimSpace(line[i+len(" • "):])
		}
		return ""
	}
	return ""
}

func (s *cliTUIState) waitFooterPermission(want string) error {
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := s.footerPermission()
		if got == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the footer names the permission mode %q, want %q; frame:\n%s", got, want, s.screenText())
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func (s *cliTUIState) operatorSwitchesPermissionMode(mode string) error {
	s.typeText("/permissions " + mode)
	s.press("\r")
	return s.waitScreen("Permission mode: "+mode, 3*time.Second)
}

func (s *cliTUIState) operatorArmsModel(model string, turns int) error {
	s.typeText(fmt.Sprintf("/model %s --count=%d", model, turns))
	s.press("\r")
	return s.waitScreen(fmt.Sprintf("Model: %s for the next %d turns", model, turns), 3*time.Second)
}

// footerShowsNoOverrides waits for the line of turn overrides to leave the
// frame.
func (s *cliTUIState) footerShowsNoOverrides() error {
	deadline := time.Now().Add(3 * time.Second)
	for {
		frame := s.screenText()
		if !strings.Contains(frame, "turns: ") && !strings.Contains(frame, "turn: ") {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the footer still names turn overrides:\n%s", frame)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

// operatorResumesSessionBefore returns to the session the operator left with
// /new, the way picking it in the /resume picker does.
func (s *cliTUIState) operatorResumesSessionBefore() error {
	id := s.prevSessionID
	if id == "" {
		return fmt.Errorf("no session was left before")
	}
	if err := s.onLoop(2*time.Second, func() { s.app.resumeInto(id) }); err != nil {
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var now string
		if err := s.onLoop(2*time.Second, func() { now = s.app.sessionID }); err != nil {
			return err
		}
		if now == id {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the console is on %q, want the resumed %q", now, id)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func initializeCLISessionSettingsScenario(sc *godog.ScenarioContext) {
	s := &cliTUIState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.shutdown()
		return ctx, nil
	})

	sc.Step(`^a coddy console app over a stub agent runner$`, s.aConsoleAppOverStubRunner)
	sc.Step(`^the console app starts$`, s.theConsoleAppStarts)
	sc.Step(`^the operator switches the permission mode to "([^"]*)"$`, s.operatorSwitchesPermissionMode)
	sc.Step(`^the footer shows the permission mode "([^"]*)"$`, s.waitFooterPermission)
	sc.Step(`^the footer shows no permission mode$`, func() error { return s.waitFooterPermission("") })
	sc.Step(`^the operator arms the model "([^"]*)" for the next (\d+) turns$`, s.operatorArmsModel)
	sc.Step(`^the footer shows "([^"]*)"$`, func(text string) error { return s.waitScreen(text, 3*time.Second) })
	sc.Step(`^the footer shows no turn overrides$`, s.footerShowsNoOverrides)
	sc.Step(`^the operator starts a new session$`, s.operatorStartsNewSession)
	sc.Step(`^the operator resumes the session before$`, s.operatorResumesSessionBefore)
}

func TestCLISessionSettingsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "cli-session-settings",
		ScenarioInitializer: initializeCLISessionSettingsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/cli_session_settings.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("cli session settings feature suite failed")
	}
}
