//go:build http

package httpserver

// Godog harness for features/session_history_empty.feature: a session opened
// and left without a prompt stays out of GET /coddy/sessions until its first
// message is saved, and a bulk delete of the whole history removes its bundle
// all the same (issue #357).

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

type sessHistoryEmptyState struct {
	sessTagsState
	// unprompted is the session opened and left without a prompt.
	unprompted string
}

func (s *sessHistoryEmptyState) openedWithoutPrompt() error {
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.root})
	if err != nil {
		return err
	}
	if _, err := os.Stat(s.store.SessionPath(res.SessionID)); err != nil {
		return fmt.Errorf("the session opened without a prompt has no bundle on disk to leave out: %w", err)
	}
	s.unprompted = res.SessionID
	return nil
}

func (s *sessHistoryEmptyState) firstPromptSaved() error {
	st := s.mgr.SessionByID(s.unprompted)
	if st == nil {
		return fmt.Errorf("session %q is not live", s.unprompted)
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "the first prompt"})
	return s.store.Save(st)
}

func (s *sessHistoryEmptyState) listed() (bool, error) {
	for _, row := range s.rows {
		if id, _ := row["id"].(string); id == s.unprompted {
			return true, nil
		}
	}
	return false, nil
}

func (s *sessHistoryEmptyState) listingLeavesOutUnprompted() error {
	if in, _ := s.listed(); in {
		return fmt.Errorf("the session left without a prompt is listed")
	}
	return nil
}

func (s *sessHistoryEmptyState) listingHoldsUnprompted() error {
	if in, _ := s.listed(); !in {
		return fmt.Errorf("the session whose first prompt was saved is not listed")
	}
	return nil
}

func (s *sessHistoryEmptyState) noBundleLeft() error {
	entries, err := os.ReadDir(s.sessRoot)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			return fmt.Errorf("a session bundle is left on disk: %s", e.Name())
		}
	}
	return nil
}

func initializeSessionHistoryEmptyScenario(sc *godog.ScenarioContext) {
	s := &sessHistoryEmptyState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.unprompted = ""
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy HTTP server$`, s.startServer)
	sc.Step(`^(\d+) stored sessions$`, s.storedSessions)
	sc.Step(`^a session that was opened and left without a prompt$`, s.openedWithoutPrompt)
	sc.Step(`^that session's first prompt is saved$`, s.firstPromptSaved)
	sc.Step(`^I list the sessions$`, func() error { return s.listQuery("") })
	sc.Step(`^I delete every session in one request$`, s.deleteEverySession)

	sc.Step(`^the listing holds sessions (\d+) and (\d+)$`, s.taggedListingHolds)
	sc.Step(`^the listing leaves out the session left without a prompt$`, s.listingLeavesOutUnprompted)
	sc.Step(`^the listing holds the session that was left without a prompt$`, s.listingHoldsUnprompted)
	sc.Step(`^no session bundle is left on disk$`, s.noBundleLeft)
}

func TestSessionHistoryEmptyFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "session-history-empty",
		ScenarioInitializer: initializeSessionHistoryEmptyScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/session_history_empty.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("session history empty feature failed")
	}
}
