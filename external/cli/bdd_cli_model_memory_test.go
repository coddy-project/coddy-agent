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

// aConsoleAppWithThreeModelsAndDefault is the same setup with a third model:
// the remembered pick, the model named on the command line and the configured
// default are then three different ones.
func (s *cliTUIState) aConsoleAppWithThreeModelsAndDefault(m1, m2, m3, def string) error {
	return s.buildAppWithModels(false, false, []config.ModelEntry{
		{Model: m1, MaxTokens: 1000, MaxContextTokens: 100000},
		{Model: m2, MaxTokens: 1000, MaxContextTokens: 100000},
		{Model: m3, MaxTokens: 1000, MaxContextTokens: 100000},
	}, def)
}

// consoleRemembersModel is what a previous run's /model left in the console's
// state file.
func (s *cliTUIState) consoleRemembersModel(id string) error {
	saveConsoleState(s.home, consoleState{LastModel: id})
	return nil
}

// writeOfModelLandsLate puts the late writer in front of the app's backend:
// the write of id into a session reaches the manager a moment after it is
// made, as a goroutine the scheduler runs late would. Before the app starts,
// so no worker reads the backend while it is swapped.
func (s *cliTUIState) writeOfModelLandsLate(id string) error {
	s.app.mgr = lateModelWrites{backend: s.app.mgr, model: id, delay: lateModelWriteDelay}
	return nil
}

// backendCanRefuseWriteOfModel puts a backend in front of the app that refuses
// the write of id into a session once the scenario says so. Before the app
// starts, like writeOfModelLandsLate.
func (s *cliTUIState) backendCanRefuseWriteOfModel(id string) error {
	s.app.mgr = failingModelWrites{backend: s.app.mgr, model: id, fail: &s.modelWritesFail}
	return nil
}

func (s *cliTUIState) backendRefusesThatWrite() error {
	s.modelWritesFail.Store(true)
	return nil
}

func (s *cliTUIState) transcriptShows(text string) error {
	return s.waitScreen(text, 3*time.Second)
}

// theConsoleAppStartsWithModel starts the console the way "coddy --model id"
// does: the session, then the command line's options, then the loop.
func (s *cliTUIState) theConsoleAppStartsWithModel(id string) error {
	return s.launchApp(func(ctx context.Context) error {
		if err := s.app.Start(ctx, "", false); err != nil {
			return err
		}
		return s.app.ApplyStartupOptions(ctx, id, "", "")
	})
}

// theConsoleAppStartsResumingWithModel is "coddy --resume --model id" over a
// folder with no session to resume: the picker opens a new one.
func (s *cliTUIState) theConsoleAppStartsResumingWithModel(id string) error {
	return s.launchApp(func(ctx context.Context) error {
		if err := s.app.Start(ctx, "", true); err != nil {
			return err
		}
		if err := s.app.ApplyStartupOptions(ctx, id, "", ""); err != nil {
			return err
		}
		s.app.populateHeader()
		return nil
	})
}

// sessionSettlesOnModel lets every write the startup started finish, then
// reads the session once. Unlike sessionStateRecordsModel it cannot pass on
// the way to another model: a late writer's overwrite is waited for.
func (s *cliTUIState) sessionSettlesOnModel(id string) error {
	s.app.JoinWorkers(5 * time.Second)
	st := s.app.mgr.SessionByID(s.app.sessionID)
	if st == nil {
		return fmt.Errorf("no live session")
	}
	if got := st.EffectiveModelID(s.cfg); got != id {
		return fmt.Errorf("session model = %q, want %q", got, id)
	}
	return nil
}

// sessionIsAlreadyOnModel reads the session once, with nothing waited for: the
// model is on it when the console app has started, not some time after.
func (s *cliTUIState) sessionIsAlreadyOnModel(id string) error {
	st := s.app.mgr.SessionByID(s.app.sessionID)
	if st == nil {
		return fmt.Errorf("no live session")
	}
	if got := st.EffectiveModelID(s.cfg); got != id {
		return fmt.Errorf("session model = %q, want %q", got, id)
	}
	return nil
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
	sc.Step(`^a coddy console app over a stub agent runner with models "([^"]*)", "([^"]*)" and "([^"]*)" and default model "([^"]*)"$`, s.aConsoleAppWithThreeModelsAndDefault)
	sc.Step(`^the console remembers the model "([^"]*)"$`, s.consoleRemembersModel)
	sc.Step(`^the write of the model "([^"]*)" into the session lands late$`, s.writeOfModelLandsLate)
	sc.Step(`^the console app starts with the model "([^"]*)" on its command line$`, s.theConsoleAppStartsWithModel)
	sc.Step(`^the console app starts with --resume and the model "([^"]*)" on its command line$`, s.theConsoleAppStartsResumingWithModel)
	sc.Step(`^the backend can refuse the write of the model "([^"]*)" into a session$`, s.backendCanRefuseWriteOfModel)
	sc.Step(`^the backend refuses that write from now on$`, s.backendRefusesThatWrite)
	sc.Step(`^the session is already on the model "([^"]*)"$`, s.sessionIsAlreadyOnModel)
	sc.Step(`^the session settles on the model "([^"]*)"$`, s.sessionSettlesOnModel)
	sc.Step(`^the transcript shows "([^"]*)"$`, s.transcriptShows)
	sc.Step(`^the footer names the model "([^"]*)"$`, s.footerNamesModel)
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
