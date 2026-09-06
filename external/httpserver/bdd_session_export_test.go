//go:build http

package httpserver

// Godog harness for features/session_export.feature: drives the built-in
// /export command over /v1/responses with the real agent runner and a canned
// LLM provider, then inspects the files written into the session workspace.

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
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type exportFeatureState struct {
	root      string
	workspace string
	ts        *httptest.Server
	mgr       *session.Manager
	srv       *Server
	sessionID string
	respText  string
	exported  string
}

func (s *exportFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-export-*")
	if err != nil {
		return err
	}
	s.root = root
	s.workspace = filepath.Join(root, "workspace")
	s.sessionID = ""
	s.respText = ""
	s.exported = ""
	return os.MkdirAll(s.workspace, 0o755)
}

func (s *exportFeatureState) close() {
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

func (s *exportFeatureState) startServer() error {
	sessRoot := filepath.Join(s.root, "sessions")
	if err := os.MkdirAll(sessRoot, 0o755); err != nil {
		return err
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: filepath.Join(s.root, "home"), CWD: s.workspace},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, Temperature: 0.2}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	fakeFactory := func(llm.ProviderInput) (llm.Provider, error) {
		return cannedSummaryProvider{}, nil
	}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		ag := agent.NewAgent(cfg, st, snd, slog.Default())
		ag.SetProviderFactory(fakeFactory)
		return ag.Run(ctx, prompt)
	}
	store := &session.FileStore{Root: sessRoot}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.workspace, store)
	s.srv = New(cfg, s.mgr, slog.Default(), s.workspace)
	s.srv.agentProviderFactory = fakeFactory
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

// sessionWithExchanges opens a session in the workspace and completes n
// prompt turns against the canned provider.
func (s *exportFeatureState) sessionWithExchanges(n int) error {
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.workspace})
	if err != nil {
		return err
	}
	s.sessionID = res.SessionID
	for i := 1; i <= n; i++ {
		if err := s.sendPrompt(fmt.Sprintf("question %d", i)); err != nil {
			return fmt.Errorf("exchange %d: %w", i, err)
		}
		if !strings.Contains(s.respText, "canned answer") {
			return fmt.Errorf("exchange %d: unexpected reply %q", i, s.respText)
		}
	}
	return nil
}

func (s *exportFeatureState) sendPrompt(prompt string) error {
	payload := map[string]interface{}{"model": "agent", "input": prompt, "stream": false}
	buf, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /v1/responses status %d", res.StatusCode)
	}
	var parsed struct {
		Output []struct {
			Text string `json:"text"`
		} `json:"output"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("decode /v1/responses body: %w", err)
	}
	s.respText = ""
	for _, o := range parsed.Output {
		s.respText += o.Text
	}
	return nil
}

func (s *exportFeatureState) responseConfirms(label string) error {
	want := "Session exported to " + label + ":"
	if !strings.Contains(s.respText, want) {
		return fmt.Errorf("response %q does not contain %q", s.respText, want)
	}
	return nil
}

// globFileExists resolves a glob pattern (relative to the workspace) to
// exactly one file and remembers it as the exported file.
func (s *exportFeatureState) globFileExists(pattern string) error {
	matches, err := filepath.Glob(filepath.Join(s.workspace, filepath.FromSlash(pattern)))
	if err != nil {
		return err
	}
	if len(matches) != 1 {
		return fmt.Errorf("pattern %q matched %d files in %s, want exactly one", pattern, len(matches), s.workspace)
	}
	s.exported = matches[0]
	return nil
}

func (s *exportFeatureState) fileExists(rel string) error {
	p := filepath.Join(s.workspace, filepath.FromSlash(rel))
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("%s is a directory", p)
	}
	s.exported = p
	return nil
}

func (s *exportFeatureState) exportedContains(want string) error {
	b, err := os.ReadFile(s.exported)
	if err != nil {
		return err
	}
	if !strings.Contains(string(b), want) {
		return fmt.Errorf("exported file %s does not contain %q", s.exported, want)
	}
	return nil
}

// transcriptAlsoHoldsToolCall appends a tool-using assistant turn straight
// to the live session, so the export can be checked without a tool-calling
// model.
func (s *exportFeatureState) transcriptAlsoHoldsToolCall(tool, result, reasoning string) error {
	st := s.mgr.SessionByID(s.sessionID)
	if st == nil {
		return fmt.Errorf("session not found")
	}
	st.AddMessage(llm.Message{
		Role:      llm.RoleAssistant,
		Reasoning: reasoning,
		ToolCalls: []llm.ToolCall{{ID: "call_bdd", Name: tool, InputJSON: `{"path":"README.md"}`}},
		Model:     "fake/model",
	})
	st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "call_bdd", Content: result})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "canned answer after the tool", Model: "fake/model"})
	return nil
}

func (s *exportFeatureState) exportedLacks(unwanted string) error {
	b, err := os.ReadFile(s.exported)
	if err != nil {
		return err
	}
	if strings.Contains(string(b), unwanted) {
		return fmt.Errorf("exported file %s still contains %q", s.exported, unwanted)
	}
	return nil
}

func (s *exportFeatureState) exportedLacksCommand() error {
	b, err := os.ReadFile(s.exported)
	if err != nil {
		return err
	}
	if strings.Contains(string(b), "/export") {
		return fmt.Errorf("exported file %s contains the /export command itself", s.exported)
	}
	return nil
}

func (s *exportFeatureState) exportedJSONEntries(n int) error {
	b, err := os.ReadFile(s.exported)
	if err != nil {
		return err
	}
	var doc struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
		Entries []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("exported JSON: %w", err)
	}
	if doc.Session.ID != s.sessionID {
		return fmt.Errorf("exported session id %q, want %q", doc.Session.ID, s.sessionID)
	}
	if len(doc.Entries) != n {
		return fmt.Errorf("exported %d entries, want %d", len(doc.Entries), n)
	}
	return nil
}

func (s *exportFeatureState) transcriptShowsExportCommand() error {
	st := s.mgr.SessionByID(s.sessionID)
	if st == nil {
		return fmt.Errorf("session not found")
	}
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleUser && strings.HasPrefix(strings.TrimSpace(m.Content), "/export") {
			return nil
		}
	}
	return fmt.Errorf("the /export command is missing from the transcript")
}

func initializeSessionExportScenario(sc *godog.ScenarioContext) {
	s := &exportFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy export server$`, s.startServer)
	sc.Step(`^an export session with (\d+) completed exchanges$`, s.sessionWithExchanges)
	sc.Step(`^the user sends "(.*)" as an export prompt$`, s.sendPrompt)
	sc.Step(`^the export response confirms a "([^"]*)" export$`, s.responseConfirms)
	sc.Step(`^a "([^"]*)" file exists in the workspace$`, s.globFileExists)
	sc.Step(`^the file "([^"]*)" exists in the workspace$`, s.fileExists)
	sc.Step(`^the exported file contains the user prompt "([^"]*)"$`, s.exportedContains)
	sc.Step(`^the exported file contains the assistant reply "([^"]*)"$`, s.exportedContains)
	sc.Step(`^the exported file contains the tool result "([^"]*)"$`, s.exportedContains)
	sc.Step(`^the exported file contains the reasoning "([^"]*)"$`, s.exportedContains)
	sc.Step(`^the exported file does not contain the "/export" command$`, s.exportedLacksCommand)
	sc.Step(`^the exported file does not contain "([^"]*)"$`, s.exportedLacks)
	sc.Step(`^the transcript also holds a "([^"]*)" tool call returning "([^"]*)" after reasoning "([^"]*)"$`, s.transcriptAlsoHoldsToolCall)
	sc.Step(`^the "/export" command is part of the transcript$`, s.transcriptShowsExportCommand)
	sc.Step(`^the exported JSON document lists (\d+) transcript entries$`, s.exportedJSONEntries)
}

func TestSessionExportFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "session_export",
		ScenarioInitializer: initializeSessionExportScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/session_export.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("session_export feature failed")
	}
}
