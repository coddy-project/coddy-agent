//go:build cli

package cli

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// aConsoleAppWithModelsAndDefault builds the console app over the stub runner
// with exactly the two configured models and the given agent.model - the
// setup where the alphabetical first differs from the configured default.
func (s *cliTUIState) aConsoleAppWithModelsAndDefault(m1, m2, def string) error {
	return s.buildAppWithModels(false, false, []config.ModelEntry{
		{Model: m1, MaxTokens: 1000, MaxContextTokens: 100000},
		{Model: m2, MaxTokens: 1000, MaxContextTokens: 100000},
	}, def)
}

// sessionStateRecordsModel polls until the live session's effective model is
// id: the initial pick and a remembered one are applied by a worker, so the
// assertion waits rather than reading once.
func (s *cliTUIState) sessionStateRecordsModel(id string) error {
	deadline := time.Now().Add(3 * time.Second)
	for {
		st := s.app.mgr.SessionByID(s.app.sessionID)
		if st != nil && st.EffectiveModelID(s.cfg) == id {
			return nil
		}
		if time.Now().After(deadline) {
			if st == nil {
				return fmt.Errorf("no live session")
			}
			return fmt.Errorf("session model = %q, want %q", st.EffectiveModelID(s.cfg), id)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func initializeCLIModelMemoryScenario(sc *godog.ScenarioContext) {
	s := &cliTUIState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.shutdown()
		return ctx, nil
	})

	sc.Step(`^a coddy console app over a stub agent runner with models "([^"]*)" and "([^"]*)" and default model "([^"]*)"$`, s.aConsoleAppWithModelsAndDefault)
	sc.Step(`^a coddy console app over a stub agent runner$`, s.aConsoleAppOverStubRunner)
	sc.Step(`^the console app starts$`, s.theConsoleAppStarts)
	sc.Step(`^the operator switches the model to "([^"]*)"$`, s.operatorSwitchesModelTo)
	sc.Step(`^the operator starts a new session$`, s.operatorStartsNewSession)
	sc.Step(`^the session state records the model "([^"]*)"$`, s.sessionStateRecordsModel)
}

func TestCLIModelMemoryFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "cli-model-memory",
		ScenarioInitializer: initializeCLIModelMemoryScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/cli_model_memory.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("cli model memory feature suite failed")
	}
}
