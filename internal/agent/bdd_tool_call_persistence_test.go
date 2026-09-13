package agent

// Godog harness for features/tool_call_persistence.feature: drives the real Agent
// through a scripted provider that hands out the tool call ids itself, then asserts
// where the record of that call landed on disk. The ids are the ones a provider
// could send and a filesystem cannot take as a folder name.

import (
	"context"
	"encoding/json"
	"fmt"
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

const tcpNotePayload = "NOTE_PAYLOAD_7f3a"

type tcpFeatureState struct {
	root       string
	cwd        string
	sessionDir string
	st         *session.State
	provider   *evScriptProvider
	toolCallID string
	answer     string
}

func (s *tcpFeatureState) reset() error {
	s.provider = &evScriptProvider{}
	s.toolCallID = ""
	s.answer = ""
	root, err := os.MkdirTemp("", "coddy-bdd-tcid-*")
	if err != nil {
		return err
	}
	s.root = root
	// The workspace and the session store are siblings under one root, so an id
	// that escapes the bundle has somewhere visible to land.
	s.cwd = filepath.Join(root, "workspace")
	s.sessionDir = filepath.Join(root, "store", "bundle")
	if err := os.MkdirAll(s.cwd, 0o755); err != nil {
		return err
	}
	return os.MkdirAll(s.sessionDir, 0o755)
}

func (s *tcpFeatureState) close() {
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
	s.st = nil
}

func (s *tcpFeatureState) bundleWithWorkspaceFile(name string) error {
	return os.WriteFile(filepath.Join(s.cwd, name), []byte(tcpNotePayload+"\n"), 0o644)
}

// readUnderID runs one turn in which the model reads a workspace file with the
// given tool call id and then answers.
func (s *tcpFeatureState) readUnderID(path, id string) error {
	s.toolCallID = id
	s.answer = "read it"
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "fake/model"},
		Tools:     config.Tools{PermissionMode: config.PermModeBypass},
	}
	s.st = &session.State{ID: "sess_bdd_toolcallid", CWD: s.cwd, Mode: session.ModeAgent, SessionDir: s.sessionDir}
	ag := NewAgent(cfg, s.st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil }
	args, _ := json.Marshal(map[string]interface{}{"path": path})
	s.provider.steps = []evStep{
		{calls: []llm.ToolCall{{ID: id, Name: "read", InputJSON: string(args)}}},
		{text: s.answer},
	}
	_, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "read the note"}})
	return err
}

func (s *tcpFeatureState) readUnderLongID(path string, n int) error {
	return s.readUnderID(path, strings.Repeat("z", n))
}

func (s *tcpFeatureState) nothingOutsideTheBundle() error {
	// Under the root only the workspace and the store; under the store only the
	// bundle. A traversal id would have created a sibling of one of them.
	rootEntries, err := entryNames(s.root)
	if err != nil {
		return err
	}
	if strings.Join(rootEntries, ",") != "store,workspace" {
		return fmt.Errorf("the root holds %v, want only the store and the workspace", rootEntries)
	}
	storeEntries, err := entryNames(filepath.Join(s.root, "store"))
	if err != nil {
		return err
	}
	if strings.Join(storeEntries, ",") != "bundle" {
		return fmt.Errorf("the store holds %v, want only the bundle", storeEntries)
	}
	workspaceEntries, err := entryNames(s.cwd)
	if err != nil {
		return err
	}
	if strings.Join(workspaceEntries, ",") != "note.txt" {
		return fmt.Errorf("the workspace holds %v, want only the file it started with", workspaceEntries)
	}
	return nil
}

func (s *tcpFeatureState) bundleHoldsOneToolCallFolder() error {
	dirs, err := session.ListToolCalls(s.sessionDir)
	if err != nil {
		return fmt.Errorf("list tool calls: %w", err)
	}
	if len(dirs) != 1 {
		return fmt.Errorf("the bundle holds %d tool call folders, want 1: %v", len(dirs), dirs)
	}
	if err := session.ValidateToolCallID(dirs[0]); err != nil {
		return fmt.Errorf("folder name %q is not a safe single path segment: %w", dirs[0], err)
	}
	return nil
}

func (s *tcpFeatureState) bundleHoldsToolCallFolder(name string) error {
	if err := s.bundleHoldsOneToolCallFolder(); err != nil {
		return err
	}
	dirs, err := session.ListToolCalls(s.sessionDir)
	if err != nil {
		return err
	}
	if dirs[0] != name {
		return fmt.Errorf("the tool call folder is %q, want %q", dirs[0], name)
	}
	return nil
}

func (s *tcpFeatureState) recordReadableUnderProviderID() error {
	args, err := session.ReadToolCallArgs(s.sessionDir, s.toolCallID)
	if err != nil {
		return fmt.Errorf("read the persisted arguments: %w", err)
	}
	if !strings.Contains(args, "note.txt") {
		return fmt.Errorf("the persisted arguments are %q, want the path the model asked for", args)
	}
	result, err := session.ReadToolCallResult(s.sessionDir, s.toolCallID)
	if err != nil {
		return fmt.Errorf("read the persisted result: %w", err)
	}
	if !strings.Contains(result, tcpNotePayload) {
		return fmt.Errorf("the persisted result is %q, want the file's content", result)
	}
	meta, err := session.ReadToolCallMeta(s.sessionDir, s.toolCallID)
	if err != nil {
		return fmt.Errorf("read the persisted metadata: %w", err)
	}
	if meta.ToolCallID != s.toolCallID {
		return fmt.Errorf("meta.json records the id as %q, want %q", meta.ToolCallID, s.toolCallID)
	}
	if meta.Status != "completed" {
		return fmt.Errorf("meta.json records the status as %q, want completed", meta.Status)
	}
	return nil
}

func (s *tcpFeatureState) turnEndedWithTheAnswer() error {
	var sawToolResult bool
	for _, m := range s.st.GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == s.toolCallID && strings.Contains(m.Content, tcpNotePayload) {
			sawToolResult = true
		}
	}
	if !sawToolResult {
		return fmt.Errorf("the transcript has no tool result for %q, so the loop lost the call", s.toolCallID)
	}
	msgs := s.st.GetMessages()
	if len(msgs) == 0 {
		return fmt.Errorf("the transcript is empty")
	}
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant || !strings.Contains(last.Content, s.answer) {
		return fmt.Errorf("the turn ended on %s %q, want the model's answer", last.Role, last.Content)
	}
	return nil
}

func entryNames(dir string) ([]string, error) {
	de, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(de))
	for _, e := range de {
		out = append(out, e.Name())
	}
	return out, nil
}

func initializeToolCallPersistenceScenario(sc *godog.ScenarioContext) {
	s := &tcpFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a session bundle in its own store and a workspace file "([^"]*)"$`, s.bundleWithWorkspaceFile)
	sc.Step(`^the model reads "([^"]*)" under the tool call id "([^"]*)"$`, s.readUnderID)
	sc.Step(`^the model reads "([^"]*)" under a tool call id of (\d+) characters$`, s.readUnderLongID)
	sc.Step(`^nothing is written outside the session bundle$`, s.nothingOutsideTheBundle)
	sc.Step(`^the bundle holds one tool call folder$`, s.bundleHoldsOneToolCallFolder)
	sc.Step(`^the bundle holds the tool call folder "([^"]*)"$`, s.bundleHoldsToolCallFolder)
	sc.Step(`^the tool call arguments and result are readable under the id the provider sent$`, s.recordReadableUnderProviderID)
	sc.Step(`^the turn ended with the model's answer$`, s.turnEndedWithTheAnswer)
}

func TestToolCallPersistenceFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "tool-call-persistence",
		ScenarioInitializer: initializeToolCallPersistenceScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/tool_call_persistence.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("tool call persistence feature suite failed")
	}
}
