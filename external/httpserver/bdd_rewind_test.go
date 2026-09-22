//go:build http

package httpserver

// Godog harness for features/session_rewind.feature: drives the live HTTP
// surface (POST /rewind, GET /messages, POST /v1/responses) to prove that a
// rewind truncates history inside the same session and the next prompt
// continues it in place.

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
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type rewindFeatureState struct {
	root     string
	sessRoot string
	ts       *httptest.Server
	mgr      *session.Manager
	srv      *Server
	store    *session.FileStore
	sid      string
	status   int
}

func (s *rewindFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-rewind-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sessRoot = filepath.Join(root, "sessions")
	s.sid = ""
	s.status = 0
	return nil
}

func (s *rewindFeatureState) close() {
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

func (s *rewindFeatureState) startServer() error {
	home := filepath.Join(s.root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(s.sessRoot, 0o755); err != nil {
		return err
	}
	// The stub plays the part of the agent turn: it records the user prompt and
	// a reply into the state, the way the real runner appends the turn's rows.
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		var text string
		for _, b := range prompt {
			text += b.Text
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: text})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "stub reply"})
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
func (s *rewindFeatureState) storedSession(n int) error {
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
	s.sid = res.SessionID
	return nil
}

// request performs an HTTP call and decodes the JSON body.
func (s *rewindFeatureState) request(method, path string, payload interface{}) (int, map[string]interface{}, error) {
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
	defer func() { _ = res.Body.Close() }()
	var parsed map[string]interface{}
	_ = json.NewDecoder(res.Body).Decode(&parsed)
	return res.StatusCode, parsed, nil
}

func (s *rewindFeatureState) rewindAt(idx int) error {
	status, body, err := s.request(http.MethodPost,
		"/coddy/sessions/"+s.sid+"/rewind",
		map[string]interface{}{"userMessageIndex": idx})
	if err != nil {
		return err
	}
	s.status = status
	if status != http.StatusOK {
		return fmt.Errorf("rewind returned %d: %v", status, body)
	}
	return nil
}

func (s *rewindFeatureState) sessionMessages() ([]map[string]interface{}, error) {
	status, body, err := s.request(http.MethodGet, "/coddy/sessions/"+s.sid+"/messages", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("messages returned %d", status)
	}
	raw, _ := body["messages"].([]interface{})
	out := make([]map[string]interface{}, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *rewindFeatureState) sessionServesMessages() error {
	status, _, err := s.request(http.MethodGet, "/coddy/sessions/"+s.sid+"/messages", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("messages returned %d", status)
	}
	return nil
}

func (s *rewindFeatureState) transcriptKeepsOnlyFirstTurn() error {
	msgs, err := s.sessionMessages()
	if err != nil {
		return err
	}
	if len(msgs) != 2 {
		return fmt.Errorf("expected 2 messages after rewind, got %d: %v", len(msgs), msgs)
	}
	if msgs[0]["role"] != "user" || msgs[1]["role"] != "assistant" {
		return fmt.Errorf("expected one user/assistant turn, got %v", msgs)
	}
	return nil
}

func (s *rewindFeatureState) sendPrompt(text string) error {
	payload := map[string]interface{}{
		"model":  "agent",
		"input":  text,
		"stream": false,
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sid)
	res, err := s.ts.Client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /v1/responses status %d", res.StatusCode)
	}
	return nil
}

func (s *rewindFeatureState) transcriptShowsAfterFirstTurn(text string) error {
	msgs, err := s.sessionMessages()
	if err != nil {
		return err
	}
	if len(msgs) < 3 {
		return fmt.Errorf("expected at least 3 messages, got %d: %v", len(msgs), msgs)
	}
	third := msgs[2]
	if third["role"] != "user" {
		return fmt.Errorf("expected a user message at the rewind point, got %v", third)
	}
	content, _ := third["content"].(string)
	if !strings.Contains(content, text) {
		return fmt.Errorf("expected %q in the message at the rewind point, got %q", text, content)
	}
	return nil
}

func TestSessionRewind(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &rewindFeatureState{}
			sc.Before(func(ctx context.Context, scd *godog.Scenario) (context.Context, error) {
				return ctx, s.reset()
			})
			sc.After(func(ctx context.Context, scd *godog.Scenario, err error) (context.Context, error) {
				s.close()
				return ctx, nil
			})
			sc.Step(`^a running coddy HTTP server$`, s.startServer)
			sc.Step(`^a stored session with (\d+) user messages$`, s.storedSession)
			sc.Step(`^I rewind the session at user message (\d+)$`, s.rewindAt)
			sc.Step(`^the session still serves its messages$`, s.sessionServesMessages)
			sc.Step(`^the transcript keeps only the first turn$`, s.transcriptKeepsOnlyFirstTurn)
			sc.Step(`^I send the prompt "([^"]*)" to the session$`, s.sendPrompt)
			sc.Step(`^the transcript shows "([^"]*)" after the first turn$`, s.transcriptShowsAfterFirstTurn)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/session_rewind.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
