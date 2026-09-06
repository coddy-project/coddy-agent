package agent

// Godog harness for features/hooks_tool_calls.feature: a real session.Manager
// over a temporary home whose hooks.json points at this test binary re-executed
// as the hook process (internal/hooks/hooktest), a scripted provider that
// issues one tool call, and a recording client. The scenarios assert what the
// model, the client and the hook process observe.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks/hooktest"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// TestHelperHook is not a real test: re-executed with the hook-helper
// positional arguments it becomes the hook process the scenarios spawn.
func TestHelperHook(t *testing.T) {
	if !hooktest.Main(flag.Args()) {
		t.Skip("helper process")
	}
}

type hooksFeatureState struct {
	root, home, cwd string
	cfg             *config.Config
	store           *session.FileStore
	mgr             *session.Manager
	sess            *session.State
	client          *recordingClient
	provider        *scriptedProvider
	permMode        string
	entries         []hooktest.Entry
	recordFile      string
	results         map[string]string
	lastResult      string
}

func (s *hooksFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-hooks-*")
	if err != nil {
		return err
	}
	s.root = root
	s.home = filepath.Join(root, "home")
	s.cwd = filepath.Join(root, "work")
	for _, d := range []string{s.home, s.cwd, filepath.Join(root, "sessions")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	s.permMode = config.PermModeBypass
	s.entries = nil
	s.recordFile = filepath.Join(root, "payload.json")
	s.results = map[string]string{}
	s.lastResult = ""
	s.sess = nil
	s.client = &recordingClient{answer: "allow"}
	return nil
}

func (s *hooksFeatureState) close() {
	if s.sess != nil && s.mgr != nil {
		s.mgr.ForgetLiveSession(s.sess.ID)
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *hooksFeatureState) addHook(event, matcher string, handler hooks.Handler) error {
	s.entries = append(s.entries, hooktest.Entry{Event: event, Matcher: matcher, Handlers: []hooks.Handler{handler}})
	return nil
}

func (s *hooksFeatureState) hookDenies(event, matcher, fragment string) error {
	return s.addHook(event, matcher, hooktest.Handler("deny", fragment))
}

func (s *hooksFeatureState) hookAllows(event, matcher string) error {
	return s.addHook(event, matcher, hooktest.Handler("allow"))
}

func (s *hooksFeatureState) hookSilent(event, matcher string) error {
	return s.addHook(event, matcher, hooktest.Handler("silent"))
}

func (s *hooksFeatureState) hookRewrites(event, matcher, command string) error {
	return s.addHook(event, matcher, hooktest.Handler("rewrite", command))
}

func (s *hooksFeatureState) hookAddsContext(event, matcher, text string) error {
	return s.addHook(event, matcher, hooktest.Handler("context", text))
}

func (s *hooksFeatureState) hookRecords(event, matcher string) error {
	return s.addHook(event, matcher, hooktest.Handler("record", s.recordFile))
}

func (s *hooksFeatureState) hookExitsTwo(event, matcher, stderr string) error {
	return s.addHook(event, matcher, hooktest.Handler("exit2", stderr))
}

func (s *hooksFeatureState) buildConfig() *config.Config {
	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.cwd, ConfigPath: filepath.Join(s.home, "config.yaml")},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 4},
		Sessions:  config.Sessions{Dir: filepath.Join(s.root, "sessions")},
	}
	cfg.Tools.PermissionMode = s.permMode
	cfg.Hooks.ApplyDefaults(cfg.Paths)
	cfg.Subagents.ApplyDefaults(cfg.Paths)
	cfg.Prompts.ApplyDefaults()
	return cfg
}

func (s *hooksFeatureState) agentSession() error {
	return s.agentSessionWithPermission(config.PermModeBypass)
}

func (s *hooksFeatureState) agentSessionWithPermission(mode string) error {
	s.permMode = mode
	if err := hooktest.Write(filepath.Join(s.home, "hooks.json"), s.entries...); err != nil {
		return err
	}
	s.cfg = s.buildConfig()
	s.store = &session.FileStore{Root: s.cfg.Sessions.Dir}
	provider := &scriptedProvider{}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		loop := NewAgent(s.cfg, st, snd, slog.Default())
		loop.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return provider, nil })
		return loop.Run(ctx, prompt)
	}
	s.mgr = session.NewManager(s.cfg, s.client, runner, slog.Default(), s.cwd, s.store)
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.cwd})
	if err != nil {
		return err
	}
	s.sess = s.mgr.SessionByID(res.SessionID)
	if s.sess == nil {
		return fmt.Errorf("session missing")
	}
	s.provider = provider
	return nil
}

func (s *hooksFeatureState) clientAnswers(answer string) error {
	s.client.mu.Lock()
	s.client.answer = answer
	s.client.mu.Unlock()
	return nil
}

// runTurn drives one turn in which the model issues call and then answers,
// and collects the tool results the model received.
func (s *hooksFeatureState) runTurn(call llm.ToolCall) error {
	if s.sess == nil {
		return fmt.Errorf("no session: add the 'an agent session' step first")
	}
	s.provider.mu.Lock()
	s.provider.steps = []scriptStep{toolStep(call), answerStep("done")}
	s.provider.calls = 0
	s.provider.mu.Unlock()
	before := len(s.sess.GetMessages())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := s.mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: s.sess.ID,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "do the hooks task"}},
	}, s.client, nil); err != nil {
		return fmt.Errorf("turn: %w", err)
	}
	for _, m := range s.sess.GetMessages()[before:] {
		if m.Role == llm.RoleTool {
			s.results[m.ToolCallID] = m.Content
		}
	}
	s.lastResult = s.results[call.ID]
	return nil
}

func (s *hooksFeatureState) modelRunsCommand(command string) error {
	return s.runTurn(commandCall("call-1", command, false))
}

func (s *hooksFeatureState) modelRunsDestructiveCommand(marker string) error {
	command := fmt.Sprintf("rm -rf %q && echo destroyed > %q", filepath.Join(s.root, "never"), filepath.Join(s.root, marker))
	return s.runTurn(commandCall("call-1", command, false))
}

func (s *hooksFeatureState) modelReadsMissingFile(name string) error {
	args, _ := json.Marshal(map[string]interface{}{"path": name})
	return s.runTurn(llm.ToolCall{ID: "call-1", Name: "read", InputJSON: string(args)})
}

func (s *hooksFeatureState) markerMissing(marker string) error {
	if _, err := os.Stat(filepath.Join(s.root, marker)); !os.IsNotExist(err) {
		return fmt.Errorf("marker file %s exists (stat err %v): the blocked command ran", marker, err)
	}
	return nil
}

func (s *hooksFeatureState) resultBlockedByHook(reason string) error {
	if !strings.Contains(s.lastResult, "blocked by hook") || !strings.Contains(s.lastResult, reason) {
		return fmt.Errorf("tool result %q does not report the hook block with reason %q", s.lastResult, reason)
	}
	return nil
}

func (s *hooksFeatureState) clientSawCancelled() error {
	s.client.mu.Lock()
	defer s.client.mu.Unlock()
	for _, u := range s.client.updates {
		if upd, ok := u.(acp.ToolCallStatusUpdate); ok && upd.ToolCallID == "call-1" && upd.Status == "cancelled" {
			return nil
		}
	}
	return fmt.Errorf("no cancelled tool_call_update for call-1 among %d updates", len(s.client.updates))
}

func (s *hooksFeatureState) resultContains(text string) error {
	if !strings.Contains(s.lastResult, text) {
		return fmt.Errorf("tool result %q lacks %q", s.lastResult, text)
	}
	return nil
}

func (s *hooksFeatureState) resultLacks(text string) error {
	if strings.Contains(s.lastResult, text) {
		return fmt.Errorf("tool result %q must not contain %q", s.lastResult, text)
	}
	return nil
}

func (s *hooksFeatureState) noPermissionRequest() error {
	if perms := s.client.permissions(); len(perms) != 0 {
		return fmt.Errorf("client received %d permission requests, want none", len(perms))
	}
	return nil
}

func (s *hooksFeatureState) permissionRequestFor(tool string) error {
	for _, p := range s.client.permissions() {
		if strings.Contains(p.ToolCall.Title, tool) {
			return nil
		}
	}
	return fmt.Errorf("no permission request for %s", tool)
}

func (s *hooksFeatureState) recordedPayload() (map[string]interface{}, error) {
	data, err := os.ReadFile(s.recordFile)
	if err != nil {
		return nil, fmt.Errorf("the recording hook did not run: %w", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("recorded payload is not JSON: %w", err)
	}
	return payload, nil
}

func (s *hooksFeatureState) payloadNames(event, tool string) error {
	payload, err := s.recordedPayload()
	if err != nil {
		return err
	}
	if payload["hook_event_name"] != event || payload["tool_name"] != tool {
		return fmt.Errorf("payload event %v tool %v, want %s %s", payload["hook_event_name"], payload["tool_name"], event, tool)
	}
	return nil
}

func (s *hooksFeatureState) payloadCarriesSession() error {
	payload, err := s.recordedPayload()
	if err != nil {
		return err
	}
	if payload["session_id"] != s.sess.ID {
		return fmt.Errorf("payload session_id %v, want %s", payload["session_id"], s.sess.ID)
	}
	if payload["cwd"] != s.sess.GetCWD() {
		return fmt.Errorf("payload cwd %v, want %s", payload["cwd"], s.sess.GetCWD())
	}
	return nil
}

func (s *hooksFeatureState) payloadCarriesCommand(command string) error {
	payload, err := s.recordedPayload()
	if err != nil {
		return err
	}
	input, _ := payload["tool_input"].(map[string]interface{})
	if input["command"] != command {
		return fmt.Errorf("payload tool_input %v, want command %q", payload["tool_input"], command)
	}
	return nil
}

func (s *hooksFeatureState) payloadCarriesError(fragment string) error {
	payload, err := s.recordedPayload()
	if err != nil {
		return err
	}
	msg, _ := payload["error"].(string)
	if !strings.Contains(msg, fragment) {
		return fmt.Errorf("payload error %q lacks %q", msg, fragment)
	}
	return nil
}

func initializeHooksScenario(sc *godog.ScenarioContext) {
	s := &hooksFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^the operator's hooks\.json has a (\w+) hook for "([^"]*)" that denies commands containing "([^"]*)"$`, s.hookDenies)
	sc.Step(`^the operator's hooks\.json has a (\w+) hook for "([^"]*)" that allows every call$`, s.hookAllows)
	sc.Step(`^the operator's hooks\.json has a (\w+) hook for "([^"]*)" that exits without a decision$`, s.hookSilent)
	sc.Step(`^the operator's hooks\.json has a (\w+) hook for "([^"]*)" that rewrites the command to "([^"]*)"$`, s.hookRewrites)
	sc.Step(`^the operator's hooks\.json has a (\w+) hook for "([^"]*)" that adds the context "([^"]*)"$`, s.hookAddsContext)
	sc.Step(`^the operator's hooks\.json has a (\w+) hook for "([^"]*)" that records its stdin$`, s.hookRecords)
	sc.Step(`^the operator's hooks\.json has a (\w+) hook for "([^"]*)" that exits with code 2 and prints "([^"]*)" to stderr$`, s.hookExitsTwo)
	sc.Step(`^an agent session$`, s.agentSession)
	sc.Step(`^an agent session in permission mode "([^"]*)"$`, s.agentSessionWithPermission)
	sc.Step(`^the client answers permission requests with "([^"]*)"$`, s.clientAnswers)

	sc.Step(`^the model runs the command "([^"]*)"$`, s.modelRunsCommand)
	sc.Step(`^the model runs a command containing "rm -rf" that would write the marker file "([^"]*)"$`, s.modelRunsDestructiveCommand)
	sc.Step(`^the model reads the missing file "([^"]*)"$`, s.modelReadsMissingFile)

	sc.Step(`^the marker file "([^"]*)" does not exist$`, s.markerMissing)
	sc.Step(`^the tool result says the call was blocked by a hook with the reason "([^"]*)"$`, s.resultBlockedByHook)
	sc.Step(`^the client saw the tool call end as cancelled$`, s.clientSawCancelled)
	sc.Step(`^the tool result contains "([^"]*)"$`, s.resultContains)
	sc.Step(`^the tool result does not contain "([^"]*)"$`, s.resultLacks)
	sc.Step(`^the client received no permission request$`, s.noPermissionRequest)
	sc.Step(`^the client received a permission request for "([^"]*)"$`, s.permissionRequestFor)
	sc.Step(`^the recorded payload names the event "([^"]*)" and the tool "([^"]*)"$`, s.payloadNames)
	sc.Step(`^the recorded payload carries the session id and the workspace path$`, s.payloadCarriesSession)
	sc.Step(`^the recorded payload carries the command "([^"]*)" as the tool input$`, s.payloadCarriesCommand)
	sc.Step(`^the recorded payload carries an error mentioning "([^"]*)"$`, s.payloadCarriesError)
}

func TestHooksFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "hooks",
		ScenarioInitializer: initializeHooksScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/hooks_tool_calls.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("hooks feature suite failed")
	}
}
