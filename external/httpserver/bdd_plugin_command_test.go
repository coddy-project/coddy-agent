//go:build http

package httpserver

// Godog harness for features/plugin_command.feature: drives the built-in
// /plugin command over /v1/responses with the real agent runner (the LLM is
// never called — /plugin is deterministic) against a local git marketplace.

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
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type pluginFeatureState struct {
	root      string
	home      string
	ts        *httptest.Server
	mgr       *session.Manager
	srv       *Server
	sessionID string
	markets   map[string]string
	respText  string
	prevHOME  string
}

func (s *pluginFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-plugin-*")
	if err != nil {
		return err
	}
	s.root = root
	s.markets = map[string]string{}
	s.respText = ""
	return nil
}

func (s *pluginFeatureState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.prevHOME != "" {
		_ = os.Setenv("CODDY_HOME", s.prevHOME)
	} else {
		_ = os.Unsetenv("CODDY_HOME")
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *pluginFeatureState) startServer() error {
	s.home = filepath.Join(s.root, "home")
	if err := os.MkdirAll(filepath.Join(s.home, "memory"), 0o755); err != nil {
		return err
	}
	s.prevHOME = os.Getenv("CODDY_HOME")
	if err := os.Setenv("CODDY_HOME", s.home); err != nil {
		return err
	}
	cfgPath := filepath.Join(s.home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("skills:\n  sources: []\n"), 0o644); err != nil {
		return err
	}
	sessRoot := filepath.Join(s.root, "sessions")
	if err := os.MkdirAll(sessRoot, 0o755); err != nil {
		return err
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.root, ConfigPath: cfgPath},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, Temperature: 0.2}},
		Agent:     config.Agent{Model: "fake/model"},
		// Scan the managed skills dir (where synced skills land) in isolation,
		// mirroring the ${CODDY_HOME}/skills default without leaking real ones.
		Skills: config.Skills{Dirs: []string{filepath.Join(s.home, "skills")}},
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

func (s *pluginFeatureState) newSession() error {
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.root})
	if err != nil {
		return err
	}
	s.sessionID = res.SessionID
	return nil
}

func (s *pluginFeatureState) marketplaceURL(name string) string {
	return "file://" + s.markets[name]
}

func (s *pluginFeatureState) givenLocalMarketplace(market, skill, version string) error {
	if !gitws.GitAvailable() {
		return fmt.Errorf("git binary not available")
	}
	repo := filepath.Join(s.root, "markets", market)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return err
	}
	s.markets[market] = repo
	if err := writeMarketplace(repo, market, skill, version); err != nil {
		return err
	}
	if err := bddGit(repo, "init", "-q"); err != nil {
		return err
	}
	if err := bddGit(repo, "config", "user.email", "t@t.io"); err != nil {
		return err
	}
	if err := bddGit(repo, "config", "user.name", "t"); err != nil {
		return err
	}
	if err := bddGit(repo, "add", "-A"); err != nil {
		return err
	}
	return bddGit(repo, "commit", "-q", "-m", "publish")
}

// sendPluginPrompt substitutes <name> tokens with the marketplace URL, then
// sends the prompt over /v1/responses and captures the assistant text.
func (s *pluginFeatureState) sendPluginPrompt(prompt string) error {
	for name, repo := range s.markets {
		prompt = strings.ReplaceAll(prompt, "<"+name+">", "file://"+repo)
	}
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

func (s *pluginFeatureState) addMarketplaceOverChat(market string) error {
	return s.sendPluginPrompt("/plugin marketplace add <" + market + ">")
}

func (s *pluginFeatureState) responseMentions(want string) error {
	if !strings.Contains(s.respText, want) {
		return fmt.Errorf("response %q does not mention %q", s.respText, want)
	}
	return nil
}

func (s *pluginFeatureState) transcriptShowsPluginCommand() error {
	st := s.mgr.SessionByID(s.sessionID)
	if st == nil {
		return fmt.Errorf("session not found")
	}
	for _, m := range st.GetMessages() {
		if strings.HasPrefix(strings.TrimSpace(m.Content), "/plugin") {
			return nil
		}
	}
	return fmt.Errorf("the /plugin command is missing from the transcript")
}

func initializePluginScenario(sc *godog.ScenarioContext) {
	s := &pluginFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy plugin server$`, s.startServer)
	sc.Step(`^a chat session$`, s.newSession)
	sc.Step(`^a local marketplace "([^"]*)" publishing skill "([^"]*)" at version "([^"]*)"$`, s.givenLocalMarketplace)
	sc.Step(`^I have added the marketplace "([^"]*)" over chat$`, s.addMarketplaceOverChat)
	sc.Step(`^I send the plugin prompt "(.*)"$`, s.sendPluginPrompt)
	sc.Step(`^the plugin response mentions "([^"]*)"$`, s.responseMentions)
	sc.Step(`^the "/plugin" command is part of the transcript$`, s.transcriptShowsPluginCommand)
}

func TestPluginCommandFeature(t *testing.T) {
	if !gitws.GitAvailable() {
		t.Skip("git binary not available")
	}
	suite := godog.TestSuite{
		Name:                "plugin_command",
		ScenarioInitializer: initializePluginScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/plugin_command.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("plugin_command feature failed")
	}
}
