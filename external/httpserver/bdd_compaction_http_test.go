//go:build http

package httpserver

// Godog harness for features/context_compaction_command.feature: drives the
// live HTTP surface (/v1/responses with the built-in /compact command and the
// POST /coddy/sessions/{id}/compact endpoint) with the real agent runner and a
// canned summarizing provider.

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

// cannedSummaryProvider answers Complete with a fixed summary and Stream with
// a fixed assistant reply.
type cannedSummaryProvider struct{}

func (cannedSummaryProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return &llm.Response{Content: "CANNED-SUMMARY of the earlier exchanges", StopReason: "end_turn"}, nil
}

func (cannedSummaryProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	onChunk(llm.StreamChunk{TextDelta: "canned answer"})
	return &llm.Response{Content: "canned answer", StopReason: "end_turn"}, nil
}

type compactHTTPFeatureState struct {
	root      string
	ts        *httptest.Server
	mgr       *session.Manager
	srv       *Server
	sessionID string
	exchanges int
	status    int
	body      map[string]interface{}
	respText  string
}

func (s *compactHTTPFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-compact-http-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sessionID = ""
	s.exchanges = 0
	s.status = 0
	s.body = nil
	s.respText = ""
	return nil
}

func (s *compactHTTPFeatureState) close() {
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

func (s *compactHTTPFeatureState) startServer() error {
	return s.startServerWithContextWindow(0)
}

// startServerWithContextWindow boots the test server; maxContextTokens > 0
// arms auto-compaction against that model context window.
func (s *compactHTTPFeatureState) startServerWithContextWindow(maxContextTokens int) error {
	sessRoot := filepath.Join(s.root, "sessions")
	if err := os.MkdirAll(sessRoot, 0o755); err != nil {
		return err
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: filepath.Join(s.root, "home"), CWD: s.root},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, Temperature: 0.2, MaxContextTokens: maxContextTokens}},
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
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.root, store)
	s.srv = New(cfg, s.mgr, slog.Default(), s.root)
	s.srv.agentProviderFactory = fakeFactory
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *compactHTTPFeatureState) sessionWithExchanges(n int) error {
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.root})
	if err != nil {
		return err
	}
	s.sessionID = res.SessionID
	st := s.mgr.SessionByID(s.sessionID)
	if st == nil {
		return fmt.Errorf("session %q not registered", s.sessionID)
	}
	for i := 1; i <= n; i++ {
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("question %d", i)})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("answer %d", i)})
	}
	s.exchanges = n
	return nil
}

func (s *compactHTTPFeatureState) sendCompactPrompt() error {
	payload := map[string]interface{}{
		"model":  "agent",
		"input":  "/compact",
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
	req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	s.status = res.StatusCode
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
	if s.status != http.StatusOK {
		return fmt.Errorf("POST /v1/responses status %d", s.status)
	}
	return nil
}

func (s *compactHTTPFeatureState) promptResponseConfirmsCompaction() error {
	if !strings.Contains(strings.ToLower(s.respText), "compacted") {
		return fmt.Errorf("response text does not confirm compaction: %q", s.respText)
	}
	return nil
}

func (s *compactHTTPFeatureState) postCompactEndpoint() error {
	buf := bytes.NewReader([]byte(`{"instructions":""}`))
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/coddy/sessions/"+s.sessionID+"/compact", buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	s.status = res.StatusCode
	s.body = nil
	var parsed map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err == nil {
		s.body = parsed
	}
	return nil
}

func (s *compactHTTPFeatureState) compactRequestSucceeds() error {
	if s.status != http.StatusOK {
		return fmt.Errorf("status = %d, body = %v", s.status, s.body)
	}
	return nil
}

func (s *compactHTTPFeatureState) compactResponseReportsSummaryAndCounts() error {
	if s.body == nil {
		return fmt.Errorf("no JSON body")
	}
	if ok, _ := s.body["compacted"].(bool); !ok {
		return fmt.Errorf("compacted != true: %v", s.body)
	}
	if sum, _ := s.body["summary"].(string); !strings.Contains(sum, "CANNED-SUMMARY") {
		return fmt.Errorf("summary missing: %v", s.body)
	}
	if n, _ := s.body["compacted_messages"].(float64); n <= 0 {
		return fmt.Errorf("compacted_messages missing: %v", s.body)
	}
	if n, _ := s.body["kept_messages"].(float64); n <= 0 {
		return fmt.Errorf("kept_messages missing: %v", s.body)
	}
	return nil
}

// transcriptJSON fetches GET /coddy/sessions/{id}/messages as raw JSON.
func (s *compactHTTPFeatureState) transcriptJSON() (string, error) {
	req, err := http.NewRequest(http.MethodGet, s.ts.URL+"/coddy/sessions/"+s.sessionID+"/messages", nil)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET messages status %d", res.StatusCode)
	}
	var b bytes.Buffer
	if _, err := b.ReadFrom(res.Body); err != nil {
		return "", err
	}
	return b.String(), nil
}

func (s *compactHTTPFeatureState) transcriptHasSummaryRow() error {
	raw, err := s.transcriptJSON()
	if err != nil {
		return err
	}
	if !strings.Contains(raw, `"compaction_summary":true`) {
		return fmt.Errorf("transcript has no compaction summary row: %s", raw)
	}
	return nil
}

func (s *compactHTTPFeatureState) transcriptKeepsAllExchanges() error {
	raw, err := s.transcriptJSON()
	if err != nil {
		return err
	}
	for i := 1; i <= s.exchanges; i++ {
		if !strings.Contains(raw, fmt.Sprintf("question %d", i)) ||
			!strings.Contains(raw, fmt.Sprintf("answer %d", i)) {
			return fmt.Errorf("transcript lost exchange %d", i)
		}
	}
	return nil
}

func (s *compactHTTPFeatureState) transcriptShowsCompactCommand() error {
	raw, err := s.transcriptJSON()
	if err != nil {
		return err
	}
	if !strings.Contains(raw, `"/compact"`) {
		return fmt.Errorf("the /compact command is missing from the transcript")
	}
	return nil
}

func initializeCompactionHTTPScenario(sc *godog.ScenarioContext) {
	s := &compactHTTPFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy HTTP server with a summarizing agent$`, s.startServer)
	sc.Step(`^an HTTP session with (\d+) completed exchanges$`, s.sessionWithExchanges)
	sc.Step(`^the user sends "/compact" as a prompt$`, s.sendCompactPrompt)
	sc.Step(`^the prompt response confirms the compaction$`, s.promptResponseConfirmsCompaction)
	sc.Step(`^the client posts to the session compact endpoint$`, s.postCompactEndpoint)
	sc.Step(`^the compact request succeeds$`, s.compactRequestSucceeds)
	sc.Step(`^the compact response reports the summary and message counts$`, s.compactResponseReportsSummaryAndCounts)
	sc.Step(`^the session transcript contains a compaction summary row$`, s.transcriptHasSummaryRow)
	sc.Step(`^the session transcript still contains all (\d+) original exchanges$`, func(int) error { return s.transcriptKeepsAllExchanges() })
	sc.Step(`^the "/compact" command is part of the transcript$`, s.transcriptShowsCompactCommand)
}

func TestContextCompactionCommandFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "context-compaction-command",
		ScenarioInitializer: initializeCompactionHTTPScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/context_compaction_command.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("context compaction command feature suite failed")
	}
}
