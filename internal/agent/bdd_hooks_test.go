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
	trustPolicy     string
	stopLoopLimit   int
	entries         []hooktest.Entry
	recordFile      string
	results         map[string]string
	lastResult      string
	turnErr         error
	stopReason      string
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
	s.trustPolicy = ""
	s.stopLoopLimit = 0
	s.turnErr = nil
	s.stopReason = ""
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
	cfg.Hooks.ProjectTrust = s.trustPolicy
	cfg.Hooks.StopLoopLimit = s.stopLoopLimit
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

// ---- project-scope files and trust (features/hooks_project_trust.feature, @acp) ----

// workspaceHook writes (or overwrites) a project-scope hooks file with one
// recording handler. The workspace exists from reset, so the file can be
// written before the session starts.
func (s *hooksFeatureState) workspaceHook(file, event, matcher string) error {
	entry := hooktest.Entry{Event: event, Matcher: matcher, Handlers: []hooks.Handler{hooktest.Handler("record", s.recordFile)}}
	return hooktest.Write(filepath.Join(s.cwd, filepath.FromSlash(file)), entry)
}

func (s *hooksFeatureState) trustPolicyIs(policy string) error {
	s.trustPolicy = policy
	return nil
}

// loadSources resolves the hook files the way the agent does for the session
// cwd, with the receipts store consulted.
func (s *hooksFeatureState) loadSources() []*hooks.Source {
	files := config.DefaultHookFiles()
	policy := s.trustPolicy
	if policy == "" {
		policy = config.ProjectTrustAsk
	}
	return hooks.NewLoader(files, policy).WithStore(hooks.NewTrustStore(s.home)).Load(s.cwd, s.home)
}

func (s *hooksFeatureState) approveFile(file string) error {
	src := hooks.FindSource(s.loadSources(), file)
	if src == nil {
		return fmt.Errorf("no hooks file %q to approve", file)
	}
	return hooks.NewTrustStore(s.home).Approve(hooks.CanonicalWorkspace(s.cwd), src)
}

func (s *hooksFeatureState) recordingHookDidNotRun() error {
	if _, err := os.Stat(s.recordFile); !os.IsNotExist(err) {
		return fmt.Errorf("the recording hook ran (stat err %v)", err)
	}
	return nil
}

func (s *hooksFeatureState) catalogLists(file, state string) error {
	for _, e := range hooks.BuildCatalog(s.loadSources()) {
		if e.File != file {
			continue
		}
		switch state {
		case "awaiting approval":
			if !e.NeedsApproval {
				return fmt.Errorf("%s should await approval, got %+v", file, e)
			}
		case "trusted":
			if !e.Trusted {
				return fmt.Errorf("%s should be trusted, got %+v", file, e)
			}
		default:
			return fmt.Errorf("unknown state %q", state)
		}
		return nil
	}
	return fmt.Errorf("catalog does not list %s", file)
}

func (s *hooksFeatureState) catalogDoesNotList(file string) error {
	for _, e := range hooks.BuildCatalog(s.loadSources()) {
		if e.File == file {
			return fmt.Errorf("catalog lists %s: %+v", file, e)
		}
	}
	return nil
}

func (s *hooksFeatureState) uiLogNotes(file, hint string) error {
	if s.countHeldNotices(file, hint) == 0 {
		return fmt.Errorf("no notice-level UI log entry mentions %q and %q: %+v", file, hint, s.sess.GetUILog())
	}
	return nil
}

func (s *hooksFeatureState) countHeldNotices(file, hint string) int {
	n := 0
	for _, e := range s.sess.GetUILog() {
		if e.Level == session.UILogLevelNotice && strings.Contains(e.Message, file) && strings.Contains(e.Message, hint) {
			n++
		}
	}
	return n
}

func (s *hooksFeatureState) noteRecordedOnce() error {
	if err := s.modelRunsCommand("echo again"); err != nil {
		return err
	}
	if n := s.countHeldNotices(".coddy/hooks.json", "coddy hooks trust"); n != 1 {
		return fmt.Errorf("the held-file notice must be recorded once per session, found %d", n)
	}
	return nil
}

func initializeHooksTrustScenario(sc *godog.ScenarioContext) {
	s := &hooksFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^the workspace's (\S+) has a (\w+) hook for "([^"]*)" that records its stdin$`, s.workspaceHook)
	sc.Step(`^the workspace's (\S+) is rewritten with a (\w+) hook for "([^"]*)" that records its stdin$`, s.workspaceHook)
	sc.Step(`^the operator approved the hook file "([^"]*)" for that workspace$`, s.approveFile)
	sc.Step(`^the hooks project trust policy is "([^"]*)"$`, s.trustPolicyIs)
	sc.Step(`^an agent session$`, s.agentSession)
	sc.Step(`^the model runs the command "([^"]*)"$`, s.modelRunsCommand)
	sc.Step(`^the recording hook did not run$`, s.recordingHookDidNotRun)
	sc.Step(`^the tool result contains "([^"]*)"$`, s.resultContains)
	sc.Step(`^the recorded payload names the event "([^"]*)" and the tool "([^"]*)"$`, s.payloadNames)
	sc.Step(`^the hooks catalog lists "([^"]*)" as (awaiting approval|trusted)$`, s.catalogLists)
	sc.Step(`^the hooks catalog does not list "([^"]*)"$`, s.catalogDoesNotList)
	sc.Step(`^the session's UI log notes that "([^"]*)" awaits approval and names "([^"]*)"$`, s.uiLogNotes)
	sc.Step(`^the note is recorded once even after a second turn$`, s.noteRecordedOnce)
}

func TestHooksTrustFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "hooks-trust",
		ScenarioInitializer: initializeHooksTrustScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/hooks_project_trust.feature"},
			Tags:     "@acp",
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("hooks trust feature suite failed")
	}
}

// ---- turn and session events (features/hooks_turn_lifecycle.feature) ----

func (s *hooksFeatureState) hookBlocks(event, reason string) error {
	return s.addHook(event, "", hooktest.Handler("block", reason))
}

func (s *hooksFeatureState) hookBlocksOnce(event, reason string) error {
	return s.addHook(event, "", hooktest.Handler("block-once", reason))
}

func (s *hooksFeatureState) hookAddsContextAny(event, text string) error {
	return s.addHook(event, "", hooktest.Handler("context", text))
}

func (s *hooksFeatureState) hookRecordsAny(event string) error {
	return s.addHook(event, "", hooktest.Handler("record", s.recordFile))
}

func (s *hooksFeatureState) stopLoopLimitIs(n int) error {
	s.stopLoopLimit = n
	return nil
}

// runPrompt drives one turn with the given prompt text and model script,
// keeping the turn's error and stop reason for the assertions instead of
// failing the step: a refused prompt is an expected outcome here.
func (s *hooksFeatureState) runPrompt(prompt string, steps ...scriptStep) error {
	if s.sess == nil {
		return fmt.Errorf("no session: add the 'an agent session' step first")
	}
	s.provider.mu.Lock()
	s.provider.steps = steps
	s.provider.calls = 0
	s.provider.requests = nil
	s.provider.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := s.mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: s.sess.ID,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: prompt}},
	}, s.client, nil)
	s.turnErr = err
	s.stopReason = ""
	if res != nil {
		s.stopReason = string(res.StopReason)
	}
	return nil
}

func (s *hooksFeatureState) userSendsPrompt(prompt string) error {
	return s.runPrompt(prompt, answerStep("ok"))
}

func (s *hooksFeatureState) turnRefusedWith(fragment string) error {
	if s.turnErr == nil || !strings.Contains(s.turnErr.Error(), fragment) {
		return fmt.Errorf("turn error %v does not mention %q", s.turnErr, fragment)
	}
	return nil
}

func (s *hooksFeatureState) modelCalls() int {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	return s.provider.calls
}

func (s *hooksFeatureState) modelNeverCalled() error {
	if n := s.modelCalls(); n != 0 {
		return fmt.Errorf("model was called %d time(s)", n)
	}
	return nil
}

func (s *hooksFeatureState) modelCalledTimes(n int) error {
	if got := s.modelCalls(); got != n {
		return fmt.Errorf("model was called %d time(s), want %d", got, n)
	}
	return nil
}

func (s *hooksFeatureState) userMessages() []string {
	var out []string
	for _, m := range s.sess.GetMessages() {
		if m.Role == llm.RoleUser {
			out = append(out, m.Content)
		}
	}
	return out
}

func (s *hooksFeatureState) transcriptHoldsNoUser(text string) error {
	for _, c := range s.userMessages() {
		if strings.Contains(c, text) {
			return fmt.Errorf("transcript holds a user message with %q", text)
		}
	}
	return nil
}

func (s *hooksFeatureState) transcriptHoldsUser(text string) error {
	for _, c := range s.userMessages() {
		if c == text {
			return nil
		}
	}
	return fmt.Errorf("transcript holds no user message %q among %q", text, s.userMessages())
}

func (s *hooksFeatureState) systemPromptContains(text string) error {
	s.provider.mu.Lock()
	reqs := append([][]llm.Message(nil), s.provider.requests...)
	s.provider.mu.Unlock()
	if len(reqs) == 0 {
		return fmt.Errorf("the model was never called")
	}
	last := reqs[len(reqs)-1]
	if len(last) == 0 || last[0].Role != llm.RoleSystem {
		return fmt.Errorf("the last request has no system message")
	}
	if !strings.Contains(last[0].Content, text) {
		return fmt.Errorf("system prompt lacks %q", text)
	}
	return nil
}

func (s *hooksFeatureState) modelAnswersThenAnswers(first, second string) error {
	return s.runPrompt("go", answerStep(first), answerStep(second))
}

func (s *hooksFeatureState) modelKeepsAnswering(text string) error {
	if text != "done" {
		return fmt.Errorf("the scripted provider only repeats %q", "done")
	}
	return s.runPrompt("go")
}

func (s *hooksFeatureState) transcriptEndsWithAssistant(text string) error {
	msgs := s.sess.GetMessages()
	if len(msgs) == 0 {
		return fmt.Errorf("empty transcript")
	}
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant || strings.TrimSpace(last.Content) != text {
		return fmt.Errorf("transcript ends with %s %q, want assistant %q", last.Role, last.Content, text)
	}
	return nil
}

func (s *hooksFeatureState) turnEndedWith(reason string) error {
	if s.turnErr != nil {
		return fmt.Errorf("turn failed: %v", s.turnErr)
	}
	if s.stopReason != reason {
		return fmt.Errorf("stop reason %q, want %q", s.stopReason, reason)
	}
	return nil
}

func (s *hooksFeatureState) bundleRecordsHookContext(text string) error {
	snap, err := s.store.ReadSnapshot(s.sess.ID)
	if err != nil {
		return err
	}
	if !strings.Contains(snap.Meta.HookContext, text) {
		return fmt.Errorf("session.json hookContext %q lacks %q", snap.Meta.HookContext, text)
	}
	return nil
}

func (s *hooksFeatureState) sessionWithLongTranscript() error {
	if err := s.agentSession(); err != nil {
		return err
	}
	for i := 1; i <= 3; i++ {
		s.sess.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("question %d", i), CreatedAt: time.Now().UTC().Format(time.RFC3339)})
		s.sess.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("answer %d", i), CreatedAt: time.Now().UTC().Format(time.RFC3339)})
	}
	return nil
}

func (s *hooksFeatureState) userRunsCompact() error {
	return s.runPrompt("/compact", answerStep("SUMMARY"))
}

func (s *hooksFeatureState) userRunsCompactWithSummary(text string) error {
	return s.runPrompt("/compact", answerStep(text))
}

func (s *hooksFeatureState) transcriptNotCompacted() error {
	for _, m := range s.sess.GetMessages() {
		if m.CompactionSummary {
			return fmt.Errorf("transcript holds a compaction summary")
		}
	}
	return nil
}

func (s *hooksFeatureState) payloadNamesEventWithTrigger(event, trigger string) error {
	payload, err := s.recordedPayload()
	if err != nil {
		return err
	}
	if payload["hook_event_name"] != event || payload["trigger"] != trigger {
		return fmt.Errorf("payload event %v trigger %v, want %s %s", payload["hook_event_name"], payload["trigger"], event, trigger)
	}
	return nil
}

func (s *hooksFeatureState) payloadCarriesSummary(fragment string) error {
	payload, err := s.recordedPayload()
	if err != nil {
		return err
	}
	summary, _ := payload["summary"].(string)
	if !strings.Contains(summary, fragment) {
		return fmt.Errorf("payload summary %q lacks %q", summary, fragment)
	}
	return nil
}

func initializeHooksTurnScenario(sc *godog.ScenarioContext) {
	s := &hooksFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^the operator's hooks\.json has a (\w+) hook that blocks with the reason "([^"]*)"$`, s.hookBlocks)
	sc.Step(`^the operator's hooks\.json has a (\w+) hook that always blocks with the reason "([^"]*)"$`, s.hookBlocks)
	sc.Step(`^the operator's hooks\.json has a (\w+) hook that blocks with the reason "([^"]*)" unless the stop hook is already active$`, s.hookBlocksOnce)
	sc.Step(`^the operator's hooks\.json has a (\w+) hook that adds the context "([^"]*)"$`, s.hookAddsContextAny)
	sc.Step(`^the operator's hooks\.json has a (\w+) hook that records its stdin$`, s.hookRecordsAny)
	sc.Step(`^the hooks stop loop limit is (\d+)$`, s.stopLoopLimitIs)
	sc.Step(`^an agent session$`, s.agentSession)
	sc.Step(`^an agent session with a long transcript$`, s.sessionWithLongTranscript)

	sc.Step(`^the user sends the prompt "([^"]*)"$`, s.userSendsPrompt)
	sc.Step(`^the model answers "([^"]*)" and, after the follow-up, answers "([^"]*)"$`, s.modelAnswersThenAnswers)
	sc.Step(`^the model keeps answering "([^"]*)"$`, s.modelKeepsAnswering)
	sc.Step(`^the user runs /compact$`, s.userRunsCompact)
	sc.Step(`^the user runs /compact and the model summarises with "([^"]*)"$`, s.userRunsCompactWithSummary)

	sc.Step(`^the turn is refused with an error mentioning "([^"]*)"$`, s.turnRefusedWith)
	sc.Step(`^the compaction is refused with an error mentioning "([^"]*)"$`, s.turnRefusedWith)
	sc.Step(`^the model was never called$`, s.modelNeverCalled)
	sc.Step(`^the model was called (\d+) times$`, s.modelCalledTimes)
	sc.Step(`^the transcript holds no user message "([^"]*)"$`, s.transcriptHoldsNoUser)
	sc.Step(`^the transcript holds the user message "([^"]*)" unchanged$`, s.transcriptHoldsUser)
	sc.Step(`^the transcript holds a user message "([^"]*)"$`, s.transcriptHoldsUser)
	sc.Step(`^the transcript ends with the assistant answer "([^"]*)"$`, s.transcriptEndsWithAssistant)
	sc.Step(`^the model's system prompt contains "([^"]*)"$`, s.systemPromptContains)
	sc.Step(`^the turn ended with the stop reason "([^"]*)"$`, s.turnEndedWith)
	sc.Step(`^the session bundle records the hook context "([^"]*)"$`, s.bundleRecordsHookContext)
	sc.Step(`^the transcript was not compacted$`, s.transcriptNotCompacted)
	sc.Step(`^the recorded payload names the event "([^"]*)" with the trigger "([^"]*)"$`, s.payloadNamesEventWithTrigger)
	sc.Step(`^the recorded payload carries a summary mentioning "([^"]*)"$`, s.payloadCarriesSummary)
}

func TestHooksTurnFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "hooks-turn",
		ScenarioInitializer: initializeHooksTurnScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/hooks_turn_lifecycle.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("hooks turn feature suite failed")
	}
}

func (s *hooksFeatureState) payloadNamesEventWithType(event, kind string) error {
	payload, err := s.recordedPayload()
	if err != nil {
		return err
	}
	if payload["hook_event_name"] != event || payload["notification_type"] != kind {
		return fmt.Errorf("payload event %v type %v, want %s %s", payload["hook_event_name"], payload["notification_type"], event, kind)
	}
	return nil
}

func initializeHooksNotificationScenario(sc *godog.ScenarioContext) {
	s := &hooksFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^the operator's hooks\.json has a (\w+) hook for "([^"]*)" that records its stdin$`, s.hookRecords)
	sc.Step(`^an agent session in permission mode "([^"]*)"$`, s.agentSessionWithPermission)
	sc.Step(`^the client answers permission requests with "([^"]*)"$`, s.clientAnswers)
	sc.Step(`^the model runs the command "([^"]*)"$`, s.modelRunsCommand)
	sc.Step(`^the recorded payload names the event "([^"]*)" with the type "([^"]*)"$`, s.payloadNamesEventWithType)
	sc.Step(`^the recorded payload carries the command "([^"]*)" as the tool input$`, s.payloadCarriesCommand)
	sc.Step(`^the tool result contains "([^"]*)"$`, s.resultContains)
}

func TestHooksNotificationFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "hooks-notification",
		ScenarioInitializer: initializeHooksNotificationScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/hooks_subagents.feature"},
			Tags:     "@notification",
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("hooks notification feature suite failed")
	}
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
