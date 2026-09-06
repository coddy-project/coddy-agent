package main

// Godog harness for features/session_export_cli.feature: drives the
// `coddy sessions export` subcommand against a temporary sessions root and
// inspects the files it writes.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type sessionsExportFeatureState struct {
	root      string
	store     *session.FileStore
	cfg       *config.Config
	state     *session.State
	sessionID string
	cwd       string
	outside   string
	stdout    bytes.Buffer
	runErr    error
	exported  string
}

func (s *sessionsExportFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-sessions-export-*")
	if err != nil {
		return err
	}
	s.root = root
	s.store = &session.FileStore{Root: filepath.Join(root, "sessions")}
	s.cfg = &config.Config{
		Paths:  config.Paths{Home: filepath.Join(root, "home")},
		Models: []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:  config.Agent{Model: "fake/model"},
	}
	s.outside = filepath.Join(root, "outside")
	s.stdout.Reset()
	s.runErr = nil
	s.exported = ""
	return os.MkdirAll(s.store.Root, 0o755)
}

func (s *sessionsExportFeatureState) close() {
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *sessionsExportFeatureState) storedSession(id string, exchanges int) error {
	dir, err := s.store.EnsureLayout(id)
	if err != nil {
		return err
	}
	s.sessionID = id
	s.state = &session.State{ID: id, CWD: filepath.Join(s.root, "workspace"), Mode: session.ModeAgent, SessionDir: dir}
	for i := 1; i <= exchanges; i++ {
		s.state.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("question %d", i), CreatedAt: "2026-09-06T10:00:00Z"})
		s.state.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("canned answer %d", i), Model: "fake/model", CreatedAt: "2026-09-06T10:00:01Z"})
	}
	return s.store.Save(s.state)
}

func (s *sessionsExportFeatureState) storedSessionAlsoHoldsToolCall(tool, result, reasoning string) error {
	s.state.AddMessage(llm.Message{
		Role:      llm.RoleAssistant,
		Reasoning: reasoning,
		ToolCalls: []llm.ToolCall{{ID: "call_bdd", Name: tool, InputJSON: `{"path":"README.md"}`}},
		Model:     "fake/model",
	})
	s.state.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "call_bdd", Content: result})
	s.state.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "canned answer after the tool", Model: "fake/model"})
	return s.store.Save(s.state)
}

func (s *sessionsExportFeatureState) emptyOutputDir() error {
	s.cwd = filepath.Join(s.root, "shell")
	return os.MkdirAll(s.cwd, 0o755)
}

// runExport splits the argument line on whitespace, substitutes <outside>
// with the directory outside the shell cwd, and runs the subcommand.
func (s *sessionsExportFeatureState) runExport(argLine string) error {
	argLine = strings.ReplaceAll(argLine, "<outside>", s.outside)
	s.stdout.Reset()
	s.runErr = sessionsExport(&s.stdout, s.store, s.cfg, s.cwd, strings.Fields(argLine))
	return nil
}

func (s *sessionsExportFeatureState) commandPrints(want string) error {
	if s.runErr != nil {
		return fmt.Errorf("command failed: %v (stdout %q)", s.runErr, s.stdout.String())
	}
	if !strings.Contains(s.stdout.String(), want) {
		return fmt.Errorf("stdout %q does not contain %q", s.stdout.String(), want)
	}
	return nil
}

func (s *sessionsExportFeatureState) globFileExists(pattern string) error {
	matches, err := filepath.Glob(filepath.Join(s.cwd, filepath.FromSlash(pattern)))
	if err != nil {
		return err
	}
	if len(matches) != 1 {
		return fmt.Errorf("pattern %q matched %d files in %s, want exactly one", pattern, len(matches), s.cwd)
	}
	s.exported = matches[0]
	return nil
}

func (s *sessionsExportFeatureState) fileExistsIn(base, rel string) error {
	p := filepath.Join(base, filepath.FromSlash(rel))
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("%s: %w (command error: %v, stdout %q)", p, err, s.runErr, s.stdout.String())
	}
	if st.IsDir() {
		return fmt.Errorf("%s is a directory", p)
	}
	s.exported = p
	return nil
}

func (s *sessionsExportFeatureState) fileExistsInOutput(rel string) error {
	return s.fileExistsIn(s.cwd, rel)
}

func (s *sessionsExportFeatureState) fileExistsOutside(rel string) error {
	return s.fileExistsIn(s.outside, rel)
}

func (s *sessionsExportFeatureState) exportedContains(want string) error {
	b, err := os.ReadFile(s.exported)
	if err != nil {
		return err
	}
	if !strings.Contains(string(b), want) {
		return fmt.Errorf("exported file %s does not contain %q", s.exported, want)
	}
	return nil
}

func (s *sessionsExportFeatureState) exportedLacks(unwanted string) error {
	b, err := os.ReadFile(s.exported)
	if err != nil {
		return err
	}
	if strings.Contains(string(b), unwanted) {
		return fmt.Errorf("exported file %s still contains %q", s.exported, unwanted)
	}
	return nil
}

func (s *sessionsExportFeatureState) exportedJSONIsSession(id string, entries int) error {
	b, err := os.ReadFile(s.exported)
	if err != nil {
		return err
	}
	var doc struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("exported JSON: %w", err)
	}
	if doc.Session.ID != id {
		return fmt.Errorf("exported session id %q, want %q", doc.Session.ID, id)
	}
	if len(doc.Entries) != entries {
		return fmt.Errorf("exported %d entries, want %d", len(doc.Entries), entries)
	}
	return nil
}

func initializeSessionsExportScenario(sc *godog.ScenarioContext) {
	s := &sessionsExportFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a sessions root holding the session "([^"]*)" with (\d+) completed exchanges$`, s.storedSession)
	sc.Step(`^the stored session also holds a "([^"]*)" tool call returning "([^"]*)" after reasoning "([^"]*)"$`, s.storedSessionAlsoHoldsToolCall)
	sc.Step(`^the shell runs in an empty output directory$`, s.emptyOutputDir)
	sc.Step(`^I run coddy sessions export "([^"]*)"$`, s.runExport)
	sc.Step(`^the command prints "([^"]*)"$`, s.commandPrints)
	sc.Step(`^a "([^"]*)" file exists in the output directory$`, s.globFileExists)
	sc.Step(`^the file "([^"]*)" exists in the output directory$`, s.fileExistsInOutput)
	sc.Step(`^the file "([^"]*)" exists outside the output directory$`, s.fileExistsOutside)
	sc.Step(`^the exported file contains "([^"]*)"$`, s.exportedContains)
	sc.Step(`^the exported file does not contain "([^"]*)"$`, s.exportedLacks)
	sc.Step(`^the exported JSON document is the session "([^"]*)" with (\d+) entries$`, s.exportedJSONIsSession)
}

func TestSessionsExportFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "session_export_cli",
		ScenarioInitializer: initializeSessionsExportScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/session_export_cli.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("session_export_cli feature failed")
	}
}
