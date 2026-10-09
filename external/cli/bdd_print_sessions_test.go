//go:build cli

package cli

// Godog harness for features/print_sessions.feature: the real `coddy -p`
// (Run, the config loader, the session store) against the scripted model of
// the prompt-input harness, and the store read back the way the pickers read
// it. Edge cases are in print_sessions_test.go.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type printSessionsScenario struct {
	t      *scenarioT
	run    *promptInputRun
	chatID string
}

func (s *printSessionsScenario) store() *session.FileStore {
	return &session.FileStore{Root: filepath.Join(s.run.home, "sessions")}
}

// manager is a manager over the run's store, the way the console opens it.
func (s *printSessionsScenario) manager() *session.Manager {
	return session.NewManager(&config.Config{}, nil, nil, slog.New(slog.DiscardHandler), s.run.work, s.store())
}

func (s *printSessionsScenario) operatorRuns(line string) error {
	args, err := splitCommandLine(line)
	if err != nil {
		return err
	}
	s.run.stdout = syncBuffer{}
	s.run.stderr = syncBuffer{}
	if err := s.run.run(args...); err != nil {
		return fmt.Errorf("coddy %s: %v (stderr %q)", line, err, s.run.stderr.String())
	}
	return nil
}

func (s *printSessionsScenario) runPrintedTheAnswer() error {
	if out := s.run.stdout.String(); !strings.Contains(out, promptInputAnswer) {
		return fmt.Errorf("stdout = %q, want the model's answer", out)
	}
	return nil
}

func (s *printSessionsScenario) allStored() ([]session.SessionListEntry, error) {
	return s.store().ListSnapshotsWith(session.ListOptions{
		IncludePrintRuns: true,
		IncludeSubagents: true,
		Archived:         session.ArchiveAll,
	})
}

func (s *printSessionsScenario) storeHoldsPrintRuns(n int) error {
	rows, err := s.allStored()
	if err != nil {
		return err
	}
	if len(rows) != n {
		return fmt.Errorf("store holds %d sessions, want %d", len(rows), n)
	}
	for _, r := range rows {
		if !session.IsPrintOrigin(r.Origin) {
			return fmt.Errorf("session %s has origin %q, want %q", r.SessionID, r.Origin, session.PrintOrigin)
		}
	}
	return nil
}

func (s *printSessionsScenario) pickerListsNoSession() error {
	cwd := s.run.work
	res, err := s.manager().HandleSessionList(context.Background(), acp.SessionListParams{CWD: &cwd})
	if err != nil {
		return err
	}
	if len(res.Sessions) != 0 {
		return fmt.Errorf("the picker lists %d sessions, want none", len(res.Sessions))
	}
	return nil
}

func (s *printSessionsScenario) sessionHoldsPrompts(first, second string) error {
	rows, err := s.allStored()
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return fmt.Errorf("store holds %d sessions, want 1", len(rows))
	}
	snap, err := s.store().ReadSnapshot(rows[0].SessionID)
	if err != nil {
		return err
	}
	var prompts []string
	for _, m := range snap.Messages {
		if m.Role == llm.RoleUser {
			prompts = append(prompts, m.Content)
		}
	}
	joined := strings.Join(prompts, "\n")
	if !strings.Contains(joined, first) || !strings.Contains(joined, second) {
		return fmt.Errorf("prompts %q, want %q and %q", prompts, first, second)
	}
	return nil
}

func (s *printSessionsScenario) consoleConversation() error {
	res, err := s.manager().HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.run.work})
	if err != nil {
		return err
	}
	s.chatID = res.SessionID
	return nil
}

func (s *printSessionsScenario) interactiveContinueOpensTheChat() error {
	id, err := latestBackendSessionID(context.Background(), s.manager(), s.run.work, false)
	if err != nil {
		return err
	}
	if id != s.chatID {
		return fmt.Errorf("coddy -c would open %s, want the console conversation %s", id, s.chatID)
	}
	return nil
}

func (s *printSessionsScenario) storeHoldsNoSession() error {
	rows, err := s.allStored()
	if err != nil {
		return err
	}
	if len(rows) != 0 {
		return fmt.Errorf("store holds %d sessions, want none", len(rows))
	}
	entries, err := os.ReadDir(s.store().Root)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			return fmt.Errorf("the sessions root still holds %s", e.Name())
		}
	}
	return nil
}

func initializePrintSessionsScenario(sc *godog.ScenarioContext) {
	s := &printSessionsScenario{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.t = &scenarioT{}
		s.run = newPromptInputRun(s.t)
		s.chatID = ""
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.t.close()
		return ctx, nil
	})
	sc.Step(`^the operator runs (coddy .+)$`, s.operatorRuns)
	sc.Step(`^the run printed the answer$`, s.runPrintedTheAnswer)
	sc.Step(`^the store holds (\d+) sessions? marked as a print run$`, s.storeHoldsPrintRuns)
	sc.Step(`^the session picker of the folder lists no session$`, s.pickerListsNoSession)
	sc.Step(`^that session holds the prompts "([^"]*)" and "([^"]*)"$`, s.sessionHoldsPrompts)
	sc.Step(`^a conversation the operator started in the console of the folder$`, s.consoleConversation)
	sc.Step(`^continuing interactively in the folder opens the console conversation$`, s.interactiveContinueOpensTheChat)
	sc.Step(`^the store holds no session$`, s.storeHoldsNoSession)
}

func TestPrintSessionsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "print-sessions",
		ScenarioInitializer: initializePrintSessionsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/print_sessions.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("print sessions feature suite failed")
	}
}
