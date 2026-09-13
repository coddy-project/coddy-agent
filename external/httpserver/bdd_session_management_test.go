//go:build http

package httpserver

// Godog harness for features/session_management.feature: drives the live HTTP
// surface of the session management table - the statistics carried by
// GET /coddy/sessions?include_stats=true and the bulk removals behind
// POST /coddy/sessions/bulk-delete.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type sessMgmtState struct {
	root     string
	sessRoot string
	ts       *httptest.Server
	mgr      *session.Manager
	srv      *Server
	store    *session.FileStore
	// ids in creation order; the feature refers to them by 1-based position.
	ids  []string
	rows []map[string]interface{}
	body map[string]interface{}
}

func (s *sessMgmtState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-sessmgmt-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sessRoot = filepath.Join(root, "sessions")
	s.ids = nil
	s.rows = nil
	s.body = nil
	return nil
}

func (s *sessMgmtState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *sessMgmtState) startServer() error {
	home := filepath.Join(s.root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(s.sessRoot, 0o755); err != nil {
		return err
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: s.root},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	s.store = &session.FileStore{Root: s.sessRoot}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.root, s.store)
	s.srv = New(cfg, s.mgr, slog.Default(), s.root)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

// storedSessions persists n bundles, each with one user turn so the row has a title.
func (s *sessMgmtState) storedSessions(n int) error {
	for i := 0; i < n; i++ {
		res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.root})
		if err != nil {
			return err
		}
		st := s.mgr.SessionByID(res.SessionID)
		if st == nil {
			return fmt.Errorf("session %q not registered", res.SessionID)
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("question %d", i+1)})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("answer %d", i+1)})
		if err := s.store.Save(st); err != nil {
			return err
		}
		s.ids = append(s.ids, res.SessionID)
	}
	return nil
}

// idAt resolves the 1-based position the feature uses into a session id.
func (s *sessMgmtState) idAt(nth int) (string, error) {
	if nth < 1 || nth > len(s.ids) {
		return "", fmt.Errorf("no session #%d (have %d)", nth, len(s.ids))
	}
	return s.ids[nth-1], nil
}

func (s *sessMgmtState) answeredByModel(nth int, model string, turns int) error {
	id, err := s.idAt(nth)
	if err != nil {
		return err
	}
	st := s.mgr.SessionByID(id)
	if st == nil {
		return fmt.Errorf("session %q not registered", id)
	}
	st.SetSelectedModelID(model)
	// The bundle already holds one turn from storedSessions; top it up to `turns`.
	for i := len(st.GetMessages()) / 2; i < turns; i++ {
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("follow up %d", i)})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("reply %d", i)})
	}
	return s.store.Save(st)
}

func (s *sessMgmtState) spentTokens(nth, in, out int) error {
	id, err := s.idAt(nth)
	if err != nil {
		return err
	}
	return session.WriteSessionStats(s.store.SessionPath(id), session.SessionStats{
		TokenUsageTotal: session.TokenUsageTotals{
			InputTokens:  in,
			OutputTokens: out,
			TotalTokens:  in + out,
		},
	})
}

// request performs an HTTP call and decodes the JSON body.
func (s *sessMgmtState) request(method, path string, payload interface{}) (int, map[string]interface{}, error) {
	var body *bytes.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(buf)
	} else {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, s.ts.URL+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	var parsed map[string]interface{}
	_ = json.NewDecoder(res.Body).Decode(&parsed)
	return res.StatusCode, parsed, nil
}

func (s *sessMgmtState) listWithStats() error {
	status, body, err := s.request(http.MethodGet, "/coddy/sessions?include_stats=true", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("session list returned %d: %v", status, body)
	}
	raw, _ := body["sessions"].([]interface{})
	s.rows = make([]map[string]interface{}, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]interface{}); ok {
			s.rows = append(s.rows, m)
		}
	}
	return nil
}

func (s *sessMgmtState) rowOf(nth int) (map[string]interface{}, error) {
	id, err := s.idAt(nth)
	if err != nil {
		return nil, err
	}
	for _, row := range s.rows {
		if row["id"] == id {
			return row, nil
		}
	}
	return nil, fmt.Errorf("session %q is not in the listing: %v", id, s.rows)
}

func (s *sessMgmtState) reportsModel(nth int, model string) error {
	row, err := s.rowOf(nth)
	if err != nil {
		return err
	}
	if got, _ := row["model"].(string); got != model {
		return fmt.Errorf("model = %q, want %q", got, model)
	}
	return nil
}

func (s *sessMgmtState) reportsMessages(nth, want int) error {
	row, err := s.rowOf(nth)
	if err != nil {
		return err
	}
	got, ok := row["messageCount"].(float64)
	if !ok {
		return fmt.Errorf("row carries no messageCount: %v", row)
	}
	if int(got) != want {
		return fmt.Errorf("messageCount = %d, want %d", int(got), want)
	}
	return nil
}

func (s *sessMgmtState) reportsTokens(nth, in, out, total int) error {
	row, err := s.rowOf(nth)
	if err != nil {
		return err
	}
	usage, ok := row["tokenUsage"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("row carries no tokenUsage: %v", row)
	}
	for field, want := range map[string]int{
		"inputTokens":  in,
		"outputTokens": out,
		"totalTokens":  total,
	} {
		got, ok := usage[field].(float64)
		if !ok {
			return fmt.Errorf("tokenUsage has no %s: %v", field, usage)
		}
		if int(got) != want {
			return fmt.Errorf("tokenUsage.%s = %d, want %d", field, int(got), want)
		}
	}
	return nil
}

func (s *sessMgmtState) everyRowReportsCreatedAt() error {
	if len(s.rows) == 0 {
		return fmt.Errorf("no rows were listed")
	}
	for _, row := range s.rows {
		if got, _ := row["createdAt"].(string); got == "" {
			return fmt.Errorf("session %v carries no createdAt: %v", row["id"], row)
		}
	}
	return nil
}

func (s *sessMgmtState) bulkDelete(payload map[string]interface{}) error {
	status, body, err := s.request(http.MethodPost, "/coddy/sessions/bulk-delete", payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("bulk delete returned %d: %v", status, body)
	}
	s.body = body
	return nil
}

func (s *sessMgmtState) deleteTicked(a, b int) error {
	first, err := s.idAt(a)
	if err != nil {
		return err
	}
	second, err := s.idAt(b)
	if err != nil {
		return err
	}
	return s.bulkDelete(map[string]interface{}{"ids": []string{first, second}})
}

func (s *sessMgmtState) deleteEverySession() error {
	return s.bulkDelete(map[string]interface{}{"scope": "all"})
}

func (s *sessMgmtState) deleteEverySessionExcept(nth int) error {
	keep, err := s.idAt(nth)
	if err != nil {
		return err
	}
	return s.bulkDelete(map[string]interface{}{"scope": "all", "except": []string{keep}})
}

func (s *sessMgmtState) reportsDeletedCount(want int) error {
	deleted, _ := s.body["deleted"].([]interface{})
	if len(deleted) != want {
		return fmt.Errorf("deleted %d sessions, want %d: %v", len(deleted), want, s.body)
	}
	if failed, _ := s.body["failed"].([]interface{}); len(failed) != 0 {
		return fmt.Errorf("bulk delete reported failures: %v", failed)
	}
	return nil
}

// remainingIDs lists what the default session listing still returns.
func (s *sessMgmtState) remainingIDs() ([]string, error) {
	status, body, err := s.request(http.MethodGet, "/coddy/sessions", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("session list returned %d: %v", status, body)
	}
	raw, _ := body["sessions"].([]interface{})
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		out = append(out, id)
	}
	return out, nil
}

func (s *sessMgmtState) onlySessionLeft(nth int) error {
	want, err := s.idAt(nth)
	if err != nil {
		return err
	}
	left, err := s.remainingIDs()
	if err != nil {
		return err
	}
	if len(left) != 1 || left[0] != want {
		return fmt.Errorf("sessions left = %v, want only %q", left, want)
	}
	return nil
}

func (s *sessMgmtState) noSessionLeft() error {
	left, err := s.remainingIDs()
	if err != nil {
		return err
	}
	if len(left) != 0 {
		return fmt.Errorf("sessions left = %v, want none", left)
	}
	return nil
}

func initializeSessionManagementScenario(sc *godog.ScenarioContext) {
	s := &sessMgmtState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy HTTP server$`, s.startServer)
	sc.Step(`^(\d+) stored sessions$`, s.storedSessions)
	sc.Step(`^session (\d+) was answered by model "([^"]*)" over (\d+) turns$`, s.answeredByModel)
	sc.Step(`^session (\d+) spent (\d+) input and (\d+) output tokens$`, s.spentTokens)

	sc.Step(`^I list sessions with statistics$`, s.listWithStats)
	sc.Step(`^I delete sessions (\d+) and (\d+) in one request$`, s.deleteTicked)
	sc.Step(`^I delete every session in one request$`, s.deleteEverySession)
	sc.Step(`^I delete every session except session (\d+)$`, s.deleteEverySessionExcept)

	sc.Step(`^session (\d+) reports model "([^"]*)"$`, s.reportsModel)
	sc.Step(`^session (\d+) reports (\d+) messages$`, s.reportsMessages)
	sc.Step(`^session (\d+) reports (\d+) input, (\d+) output and (\d+) total tokens$`, s.reportsTokens)
	sc.Step(`^every listed session reports when it was created$`, s.everyRowReportsCreatedAt)
	sc.Step(`^the response reports (\d+) deleted sessions$`, s.reportsDeletedCount)
	sc.Step(`^only session (\d+) is left$`, s.onlySessionLeft)
	sc.Step(`^no session is left$`, s.noSessionLeft)
}

func TestSessionManagementFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "session-management",
		ScenarioInitializer: initializeSessionManagementScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/session_management.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("session management feature failed")
	}
}
