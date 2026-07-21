//go:build http

package httpserver

// Godog harness for features/remote_api.feature: proves a remote, authenticated
// coddy HTTP server behaves like a local one. Every request goes over the real
// HTTP surface through the bearer-auth gate; a stub runner keeps it LLM-free.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// promptBlocksText joins the text of prompt content blocks (mirrors the real agent's view).
func promptBlocksText(blocks []acp.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

func errUnknownFolder(name string) error { return fmt.Errorf("unknown folder %q", name) }

func errStatus(msg string, status int, body string) error {
	if len(body) > 400 {
		body = body[:400] + "…"
	}
	return fmt.Errorf("%s (status %d, body: %s)", msg, status, body)
}

type remoteFeatureState struct {
	root      string
	sessRoot  string
	ts        *httptest.Server
	mgr       *session.Manager
	srv       *Server
	token     string // token the server requires ("" = local/no-auth)
	bearer    string // token the client presents ("" = none)
	folders   map[string]string
	sessionID string
	status    int
	rawBody   string
	body      map[string]interface{}
}

func (s *remoteFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-remote-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sessRoot = filepath.Join(root, "sessions")
	s.folders = map[string]string{}
	s.token = ""
	s.bearer = ""
	s.sessionID = ""
	s.status = 0
	s.rawBody = ""
	s.body = nil
	return nil
}

func (s *remoteFeatureState) close() {
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

func (s *remoteFeatureState) start(token string) error {
	s.token = token
	home := filepath.Join(s.root, "home")
	if err := os.MkdirAll(filepath.Join(home, "memory"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(s.sessRoot, 0o755); err != nil {
		return err
	}
	// Stub runner completes the turn without contacting an LLM. Like the real agent, it
	// records the user turn so transcripts load over the API.
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		if text := strings.TrimSpace(promptBlocksText(prompt)); text != "" {
			st.AddMessage(llm.Message{Role: llm.RoleUser, Content: text})
		}
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:      config.Paths{Home: home, CWD: s.root},
		Models:     []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:      config.Agent{Model: "openai/gpt-4o"},
		HTTPServer: config.HTTPServerConfig{AuthToken: token},
	}
	store := &session.FileStore{Root: s.sessRoot}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.root, store)
	s.srv = New(cfg, s.mgr, slog.Default(), s.root)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *remoteFeatureState) startAuthed(token string) error { return s.start(token) }
func (s *remoteFeatureState) startLocal() error              { return s.start("") }

func (s *remoteFeatureState) presentToken() error   { s.bearer = s.token; return nil }
func (s *remoteFeatureState) presentNoToken() error { s.bearer = ""; return nil }
func (s *remoteFeatureState) presentInvalid() error { s.bearer = "not-the-token"; return nil }

func (s *remoteFeatureState) plainFolder(name string) error {
	dir := filepath.Join(s.root, "ws", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	s.folders[name] = dir
	return nil
}

func (s *remoteFeatureState) sessionRootedAt(name string) error {
	dir, ok := s.folders[name]
	if !ok {
		return errUnknownFolder(name)
	}
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: dir})
	if err != nil {
		return err
	}
	s.sessionID = res.SessionID
	return nil
}

func (s *remoteFeatureState) do(req *http.Request) error {
	if s.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+s.bearer)
	}
	if s.sessionID != "" {
		req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	s.status = res.StatusCode
	raw, _ := io.ReadAll(res.Body)
	s.rawBody = string(raw)
	s.body = nil
	var parsed map[string]interface{}
	if json.Unmarshal(raw, &parsed) == nil {
		s.body = parsed
	}
	return nil
}

func (s *remoteFeatureState) get(path string) error {
	req, err := http.NewRequest(http.MethodGet, s.ts.URL+path, nil)
	if err != nil {
		return err
	}
	return s.do(req)
}

func (s *remoteFeatureState) requestModels() error  { return s.get("/v1/models") }
func (s *remoteFeatureState) listSessions() error   { return s.get("/coddy/sessions") }
func (s *remoteFeatureState) requestConfig() error  { return s.get("/coddy/config") }
func (s *remoteFeatureState) requestContext() error { return s.get("/coddy/workspace/context") }
func (s *remoteFeatureState) requestMessages() error {
	return s.get("/coddy/sessions/" + s.sessionID + "/messages")
}

func (s *remoteFeatureState) switchWorkspace(name string) error {
	dir, ok := s.folders[name]
	if !ok {
		return errUnknownFolder(name)
	}
	buf, _ := json.Marshal(map[string]interface{}{"path": dir})
	req, err := http.NewRequest(http.MethodPost,
		s.ts.URL+"/coddy/sessions/"+s.sessionID+"/workspace", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return s.do(req)
}

func (s *remoteFeatureState) sendPrompt(text string) error {
	payload := map[string]interface{}{"model": "agent", "input": text, "stream": true}
	buf, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return s.do(req)
}

// ---- assertions ----

func (s *remoteFeatureState) unauthorized() error {
	if s.status != http.StatusUnauthorized {
		return errStatus("want 401", s.status, s.rawBody)
	}
	return nil
}

func (s *remoteFeatureState) succeeds() error {
	if s.status < 200 || s.status >= 300 {
		return errStatus("want 2xx", s.status, s.rawBody)
	}
	return nil
}

func (s *remoteFeatureState) modelListIncludesProfiles(list string) error {
	rows, _ := s.body["data"].([]interface{})
	have := map[string]bool{}
	for _, r := range rows {
		if m, ok := r.(map[string]interface{}); ok {
			if id, ok := m["id"].(string); ok {
				have[id] = true
			}
		}
	}
	for _, want := range bddSplitList(list) {
		if !have[want] {
			return errStatus("model list missing "+want, s.status, s.rawBody)
		}
	}
	return nil
}

func (s *remoteFeatureState) contextPathPointsTo(name string) error {
	dir, ok := s.folders[name]
	if !ok {
		return errUnknownFolder(name)
	}
	if err := s.requestContext(); err != nil {
		return err
	}
	if err := s.succeeds(); err != nil {
		return err
	}
	got, _ := s.body["path"].(string)
	if bddNormPath(got) != bddNormPath(dir) {
		return errStatus("context path = "+got+", want "+dir, s.status, s.rawBody)
	}
	return nil
}

func (s *remoteFeatureState) cwdPersistedAs(name string) error {
	dir, ok := s.folders[name]
	if !ok {
		return errUnknownFolder(name)
	}
	raw, err := os.ReadFile(filepath.Join(s.sessRoot, s.sessionID, "session.json"))
	if err != nil {
		return err
	}
	var meta struct {
		CWD string `json:"cwd"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return err
	}
	if bddNormPath(meta.CWD) != bddNormPath(dir) {
		return errStatus("persisted cwd = "+meta.CWD+", want "+dir, s.status, s.rawBody)
	}
	return nil
}

func (s *remoteFeatureState) streamedResponseTerminates() error {
	if !strings.Contains(s.rawBody, "[DONE]") {
		return errStatus("streamed response missing [DONE]", s.status, s.rawBody)
	}
	return nil
}

func (s *remoteFeatureState) transcriptIncludesPrompt(text string) error {
	if err := s.requestMessages(); err != nil {
		return err
	}
	if err := s.succeeds(); err != nil {
		return err
	}
	if !strings.Contains(s.rawBody, text) {
		return errStatus("transcript missing prompt "+text, s.status, s.rawBody)
	}
	return nil
}

func (s *remoteFeatureState) sessionListIncludesSession() error {
	if !strings.Contains(s.rawBody, s.sessionID) {
		return errStatus("session list missing "+s.sessionID, s.status, s.rawBody)
	}
	return nil
}

func (s *remoteFeatureState) configHidesToken() error {
	if s.token != "" && strings.Contains(s.rawBody, s.token) {
		return errStatus("config leaked the auth token", s.status, s.rawBody)
	}
	return nil
}

func (s *remoteFeatureState) configReportsAuthConfigured() error {
	hs, _ := s.body["httpserver"].(map[string]interface{})
	if hs == nil {
		return errStatus("config has no httpserver block", s.status, s.rawBody)
	}
	if configured, _ := hs["auth_configured"].(bool); !configured {
		return errStatus("config does not report auth_configured", s.status, s.rawBody)
	}
	return nil
}

func initializeRemoteScenario(sc *godog.ScenarioContext) {
	s := &remoteFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^an authenticated coddy HTTP server with token "([^"]+)"$`, s.startAuthed)
	sc.Step(`^a local coddy HTTP server without authentication$`, s.startLocal)
	sc.Step(`^the client presents the token$`, s.presentToken)
	sc.Step(`^the client presents no token$`, s.presentNoToken)
	sc.Step(`^the client presents an invalid token$`, s.presentInvalid)
	sc.Step(`^a workspace folder "([^"]+)"$`, s.plainFolder)
	sc.Step(`^a session rooted at folder "([^"]+)"$`, s.sessionRootedAt)

	sc.Step(`^I request the model list$`, s.requestModels)
	sc.Step(`^I switch the session workspace to folder "([^"]+)"$`, s.switchWorkspace)
	sc.Step(`^I send the prompt "([^"]+)" to the session$`, s.sendPrompt)
	sc.Step(`^I list sessions$`, s.listSessions)
	sc.Step(`^I request the server config$`, s.requestConfig)

	sc.Step(`^the request is rejected as unauthorized$`, s.unauthorized)
	sc.Step(`^the request succeeds$`, s.succeeds)
	sc.Step(`^the model list includes profiles "([^"]+)"$`, s.modelListIncludesProfiles)
	sc.Step(`^the context path points to folder "([^"]+)"$`, s.contextPathPointsTo)
	sc.Step(`^the session cwd is persisted as folder "([^"]+)"$`, s.cwdPersistedAs)
	sc.Step(`^the streamed response terminates cleanly$`, s.streamedResponseTerminates)
	sc.Step(`^the session transcript includes the prompt "([^"]+)"$`, s.transcriptIncludesPrompt)
	sc.Step(`^the session list includes the session$`, s.sessionListIncludesSession)
	sc.Step(`^the config response hides the auth token$`, s.configHidesToken)
	sc.Step(`^the config response reports authentication is configured$`, s.configReportsAuthConfigured)
}

func TestRemoteAPIFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "remote-api",
		ScenarioInitializer: initializeRemoteScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features/remote_api.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("remote API feature suite failed")
	}
}
