//go:build http

package httpserver

// Godog harness for features/session_branch_delete.feature: drives the live HTTP
// surface (branch creation, session delete, branch listing) to prove that a
// deleted branch stops being advertised by the session it forked from.

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

type brDelState struct {
	root     string
	sessRoot string
	ts       *httptest.Server
	mgr      *session.Manager
	srv      *Server
	store    *session.FileStore
	parentID string
	branches []string
}

func (s *brDelState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-brdel-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sessRoot = filepath.Join(root, "sessions")
	s.parentID = ""
	s.branches = nil
	return nil
}

func (s *brDelState) close() {
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

func (s *brDelState) startServer() error {
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

// storedSession persists a session bundle with n alternating user/assistant turns.
func (s *brDelState) storedSession(n int) error {
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.root})
	if err != nil {
		return err
	}
	st := s.mgr.SessionByID(res.SessionID)
	if st == nil {
		return fmt.Errorf("session %q not registered", res.SessionID)
	}
	for i := 0; i < n; i++ {
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("ask %d", i)})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("answer %d", i)})
	}
	if err := s.store.Save(st); err != nil {
		return err
	}
	s.parentID = res.SessionID
	return nil
}

// request performs an HTTP call and decodes the JSON body.
func (s *brDelState) request(method, path string, payload interface{}) (int, map[string]interface{}, error) {
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

func (s *brDelState) branchAt(idx int) error {
	status, body, err := s.request(http.MethodPost,
		"/coddy/sessions/"+s.parentID+"/branches",
		map[string]interface{}{"userMessageIndex": idx})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("branch create returned %d: %v", status, body)
	}
	newID, _ := body["newSessionId"].(string)
	if newID == "" {
		return fmt.Errorf("branch create returned no session id: %v", body)
	}
	s.branches = append(s.branches, newID)
	return nil
}

func (s *brDelState) deleteBranch(nth int) error {
	if nth >= len(s.branches) {
		return fmt.Errorf("no branch #%d (have %d)", nth, len(s.branches))
	}
	id := s.branches[nth]
	status, body, err := s.request(http.MethodDelete, "/coddy/sessions/"+id, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("delete returned %d: %v", status, body)
	}
	return nil
}

func (s *brDelState) deleteOnlyBranch() error  { return s.deleteBranch(len(s.branches) - 1) }
func (s *brDelState) deleteFirstBranch() error { return s.deleteBranch(0) }

// branchPoints lists the branch points a session advertises.
func (s *brDelState) branchPoints(id string) ([]map[string]interface{}, error) {
	status, body, err := s.request(http.MethodGet, "/coddy/sessions/"+id+"/branches", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("branch list for %q returned %d: %v", id, status, body)
	}
	raw, _ := body["branchPoints"].([]interface{})
	out := make([]map[string]interface{}, 0, len(raw))
	for _, bp := range raw {
		if m, ok := bp.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *brDelState) parentReportsNoBranchPoints() error {
	points, err := s.branchPoints(s.parentID)
	if err != nil {
		return err
	}
	if len(points) != 0 {
		return fmt.Errorf("parent still reports branch points: %v", points)
	}
	return nil
}

func (s *brDelState) parentReportsThreadsAt(total, idx int) error {
	points, err := s.branchPoints(s.parentID)
	if err != nil {
		return err
	}
	for _, bp := range points {
		if int(bp["userMessageIndex"].(float64)) != idx {
			continue
		}
		if got := int(bp["total"].(float64)); got != total {
			return fmt.Errorf("branch point at %d: total = %d, want %d", idx, got, total)
		}
		return nil
	}
	return fmt.Errorf("no branch point at user message %d: %v", idx, points)
}

func (s *brDelState) noBranchPointReferencesDeleted() error {
	deleted := s.branches[0]
	for _, id := range append([]string{s.parentID}, s.branches[1:]...) {
		points, err := s.branchPoints(id)
		if err != nil {
			return err
		}
		for _, bp := range points {
			sessions, _ := bp["sessions"].([]interface{})
			for _, raw := range sessions {
				m, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				if m["sessionId"] == deleted {
					return fmt.Errorf("session %q still lists deleted branch %q", id, deleted)
				}
			}
		}
	}
	return nil
}

func (s *brDelState) survivingBranchPosition(pos, total int) error {
	survivor := s.branches[len(s.branches)-1]
	points, err := s.branchPoints(survivor)
	if err != nil {
		return err
	}
	if len(points) != 1 {
		return fmt.Errorf("surviving branch: want 1 branch point, got %v", points)
	}
	gotTotal := int(points[0]["total"].(float64))
	gotPos := int(points[0]["currentIndex"].(float64)) + 1
	if gotPos != pos || gotTotal != total {
		return fmt.Errorf("surviving branch at %d of %d, want %d of %d", gotPos, gotTotal, pos, total)
	}
	sessions, _ := points[0]["sessions"].([]interface{})
	if gotPos > len(sessions) {
		return fmt.Errorf("currentIndex %d out of range for %d sessions", gotPos-1, len(sessions))
	}
	m, _ := sessions[gotPos-1].(map[string]interface{})
	if m["sessionId"] != survivor {
		return fmt.Errorf("position %d holds %v, want %q", gotPos, m["sessionId"], survivor)
	}
	return nil
}

func (s *brDelState) parentServesMessages() error {
	status, body, err := s.request(http.MethodGet, "/coddy/sessions/"+s.parentID+"/messages", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("messages returned %d: %v", status, body)
	}
	msgs, _ := body["messages"].([]interface{})
	if len(msgs) == 0 {
		return fmt.Errorf("parent session served no messages: %v", body)
	}
	return nil
}

func initializeBranchDeleteScenario(sc *godog.ScenarioContext) {
	s := &brDelState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy HTTP server$`, s.startServer)
	sc.Step(`^a stored session with (\d+) user messages$`, s.storedSession)
	sc.Step(`^the session is branched at user message (\d+)$`, s.branchAt)

	sc.Step(`^I delete the branch session$`, s.deleteOnlyBranch)
	sc.Step(`^I delete the first branch session$`, s.deleteFirstBranch)

	sc.Step(`^the parent session reports no branch points$`, s.parentReportsNoBranchPoints)
	sc.Step(`^the parent session reports (\d+) threads at user message (\d+)$`, s.parentReportsThreadsAt)
	sc.Step(`^no branch point references the deleted session$`, s.noBranchPointReferencesDeleted)
	sc.Step(`^the surviving branch reports position (\d+) of (\d+)$`, s.survivingBranchPosition)
	sc.Step(`^the parent session still serves its messages$`, s.parentServesMessages)
}

func TestSessionBranchDeleteFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "session-branch-delete",
		ScenarioInitializer: initializeBranchDeleteScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/session_branch_delete.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("session branch delete feature suite failed")
	}
}
