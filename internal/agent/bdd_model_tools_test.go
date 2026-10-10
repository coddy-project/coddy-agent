package agent

// Godog harness for features/model_tool_restrictions.feature: drives the real
// Agent.Run in agent mode with a fake LLM provider on a models[] row that
// carries tools / disallowed_tools. The scenarios read what the provider was
// sent (the request's tool list and the tool list in the system message) and
// replay a call to a tool the row does not offer, which has to be refused
// before any permission prompt; a control scenario runs the same call on a row
// with no lists, so the refusal is provably the only thing between the call and
// the file.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	bddModelToolsAnswer   = "Answered without touching the repository."
	bddModelToolsCallID   = "call_unlisted"
	bddModelToolsFileBody = "written by the model"
)

// bddModelToolsPromptLine matches a line of the tool list the system prompt
// prints ("- `name`: description"), the shape tools.FormatDefinitionsForPrompt
// writes.
var bddModelToolsPromptLine = regexp.MustCompile("(?m)^- `([a-z0-9_]+(?:__[a-zA-Z0-9_.-]+)?)`: ")

// bddModelToolsProvider answers directly, or requests one tool call on its
// first step and answers on the next, recording what each request carried.
type bddModelToolsProvider struct {
	toolCall *llm.ToolCall
	calls    int
	offered  [][]llm.ToolDefinition
	seen     [][]llm.Message
}

func (p *bddModelToolsProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("Complete must not be used by the model tool restrictions suite")
}

func (p *bddModelToolsProvider) Stream(_ context.Context, messages []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	p.offered = append(p.offered, append([]llm.ToolDefinition(nil), defs...))
	p.seen = append(p.seen, append([]llm.Message(nil), messages...))
	if p.toolCall != nil && p.calls == 1 {
		tc := *p.toolCall
		onChunk(llm.StreamChunk{ToolCall: &tc})
		return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}, nil
	}
	onChunk(llm.StreamChunk{TextDelta: bddModelToolsAnswer})
	return &llm.Response{Content: bddModelToolsAnswer, StopReason: "end_turn"}, nil
}

// bddModelToolsSender answers every permission prompt with allow and counts the
// prompts and the tool-call updates.
type bddModelToolsSender struct {
	recordingPermissionSender
	updates []interface{}
}

func (s *bddModelToolsSender) SendSessionUpdate(_ string, update interface{}) error {
	s.updates = append(s.updates, update)
	return nil
}

type modelToolsFeatureState struct {
	tmpDirs  []string
	st       *session.State
	ag       *Agent
	cfg      *config.Config
	provider *bddModelToolsProvider
	sender   *bddModelToolsSender
	target   string

	stop   string
	runErr error
}

func (s *modelToolsFeatureState) reset() error {
	s.close()
	s.sender = &bddModelToolsSender{}
	s.provider = nil
	s.target = ""
	s.stop = ""
	s.runErr = nil
	return nil
}

func (s *modelToolsFeatureState) close() {
	for _, d := range s.tmpDirs {
		_ = os.RemoveAll(d)
	}
	s.tmpDirs = nil
	s.st = nil
	s.ag = nil
	s.cfg = nil
}

func (s *modelToolsFeatureState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-model-tools-*")
	if err != nil {
		return "", err
	}
	s.tmpDirs = append(s.tmpDirs, d)
	return d, nil
}

func splitToolList(list string) []string {
	var out []string
	for _, part := range strings.Split(list, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *modelToolsFeatureState) sessionOn(entry config.ModelEntry) error {
	cwd, err := s.tempDir()
	if err != nil {
		return err
	}
	sessionDir, err := s.tempDir()
	if err != nil {
		return err
	}
	s.target = filepath.Join(cwd, "notes.md")
	s.st = &session.State{ID: "sess_bdd_model_tools", CWD: cwd, Mode: session.ModeAgent, SessionDir: sessionDir}
	entry.Model = "fake/model"
	entry.MaxTokens = 100
	s.cfg = &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{entry},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 6},
	}
	return nil
}

func (s *modelToolsFeatureState) sessionWithTools(list string) error {
	return s.sessionOn(config.ModelEntry{Tools: splitToolList(list)})
}

func (s *modelToolsFeatureState) sessionWithDisallowed(list string) error {
	return s.sessionOn(config.ModelEntry{DisallowedTools: splitToolList(list)})
}

func (s *modelToolsFeatureState) sessionWithoutLists() error {
	return s.sessionOn(config.ModelEntry{})
}

func (s *modelToolsFeatureState) buildAgent() error {
	if s.st == nil {
		return fmt.Errorf("no session prepared")
	}
	s.ag = NewAgent(s.cfg, s.st, s.sender, nil)
	s.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil }
	return nil
}

func (s *modelToolsFeatureState) modelAnswersDirectly() error {
	s.provider = &bddModelToolsProvider{}
	return s.buildAgent()
}

func (s *modelToolsFeatureState) modelRequestsToolOnce(tool string) error {
	if tool != "write" {
		return fmt.Errorf("the suite only simulates the write tool, got %q", tool)
	}
	args, err := json.Marshal(map[string]string{"path": s.target, "content": bddModelToolsFileBody})
	if err != nil {
		return err
	}
	s.provider = &bddModelToolsProvider{toolCall: &llm.ToolCall{ID: bddModelToolsCallID, Name: tool, InputJSON: string(args)}}
	return s.buildAgent()
}

func (s *modelToolsFeatureState) userSendsMessage() error {
	if s.ag == nil {
		return fmt.Errorf("no agent prepared")
	}
	s.stop, s.runErr = s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "what does this repository do?"}})
	return nil
}

func (s *modelToolsFeatureState) firstRequest() ([]llm.ToolDefinition, string, error) {
	if s.provider == nil || len(s.provider.offered) == 0 {
		return nil, "", fmt.Errorf("the model was never called")
	}
	system := ""
	if msgs := s.provider.seen[0]; len(msgs) > 0 && msgs[0].Role == llm.RoleSystem {
		system = msgs[0].Content
	}
	return s.provider.offered[0], system, nil
}

func sortedNames(defs []llm.ToolDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	sort.Strings(names)
	return names
}

func promptToolNames(system string) []string {
	var names []string
	for _, m := range bddModelToolsPromptLine.FindAllStringSubmatch(system, -1) {
		names = append(names, m[1])
	}
	sort.Strings(names)
	return names
}

func (s *modelToolsFeatureState) offeredExactly(list string) error {
	defs, _, err := s.firstRequest()
	if err != nil {
		return err
	}
	want := splitToolList(list)
	sort.Strings(want)
	if got := sortedNames(defs); strings.Join(got, ",") != strings.Join(want, ",") {
		return fmt.Errorf("request tools = %v, want exactly %v", got, want)
	}
	return nil
}

func (s *modelToolsFeatureState) promptListsExactly(list string) error {
	_, system, err := s.firstRequest()
	if err != nil {
		return err
	}
	want := splitToolList(list)
	sort.Strings(want)
	if got := promptToolNames(system); strings.Join(got, ",") != strings.Join(want, ",") {
		return fmt.Errorf("system prompt lists %v, want exactly %v", got, want)
	}
	return nil
}

// fullSetWithout checks the request against what the same session offers
// before the model's lists apply, minus the named tools, and that the full set
// is the broad one (the tools the lists exist to spare a small model).
func (s *modelToolsFeatureState) fullSetWithout(list string) error {
	defs, _, err := s.firstRequest()
	if err != nil {
		return err
	}
	skip := map[string]bool{}
	for _, n := range splitToolList(list) {
		skip[n] = true
	}
	var want []string
	for _, d := range s.ag.sessionToolDefinitions("agent") {
		if !skip[d.Name] {
			want = append(want, d.Name)
		}
	}
	sort.Strings(want)
	got := sortedNames(defs)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		return fmt.Errorf("request tools = %v, want the full set minus %v = %v", got, list, want)
	}
	if len(got) < 30 {
		return fmt.Errorf("the full set holds only %d tools: %v", len(got), got)
	}
	for _, n := range []string{"read", "grep", "write", "edit", "glob"} {
		if !skip[n] && !contains(got, n) {
			return fmt.Errorf("tool %q missing from the full set %v", n, got)
		}
	}
	for n := range skip {
		if contains(got, n) {
			return fmt.Errorf("tool %q was removed by the lists but is still offered", n)
		}
	}
	return nil
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func (s *modelToolsFeatureState) promptListsSameTools() error {
	defs, system, err := s.firstRequest()
	if err != nil {
		return err
	}
	want, got := sortedNames(defs), promptToolNames(system)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		return fmt.Errorf("system prompt lists %v, the request carries %v", got, want)
	}
	return nil
}

func (s *modelToolsFeatureState) fileIsNotWritten() error {
	if _, err := os.Stat(s.target); err == nil {
		return fmt.Errorf("%s exists: the refused write ran", s.target)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *modelToolsFeatureState) fileIsWritten() error {
	body, err := os.ReadFile(s.target)
	if err != nil {
		return fmt.Errorf("the write did not happen: %v", err)
	}
	if string(body) != bddModelToolsFileBody {
		return fmt.Errorf("unexpected file body %q", body)
	}
	return nil
}

func (s *modelToolsFeatureState) callAnsweredWithModelRefusal() error {
	var toolMsg *llm.Message
	for _, m := range s.st.GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == bddModelToolsCallID {
			mm := m
			toolMsg = &mm
			break
		}
	}
	if toolMsg == nil {
		return fmt.Errorf("no tool result recorded for %s", bddModelToolsCallID)
	}
	for _, want := range []string{`tool "write" is not available on model fake/model`, "tools and disallowed_tools"} {
		if !strings.Contains(toolMsg.Content, want) {
			return fmt.Errorf("tool result %q does not say %q", toolMsg.Content, want)
		}
	}
	cancelled := false
	for _, u := range s.sender.updates {
		if up, ok := u.(acp.ToolCallStatusUpdate); ok && up.ToolCallID == bddModelToolsCallID && up.Status == "cancelled" {
			cancelled = true
		}
	}
	if !cancelled {
		return fmt.Errorf("no cancelled tool_call_update was sent for %s", bddModelToolsCallID)
	}
	if len(s.provider.seen) < 2 {
		return fmt.Errorf("the model was not re-prompted after the refusal")
	}
	return nil
}

func (s *modelToolsFeatureState) noPermissionPrompt() error {
	if n := len(s.sender.requests); n != 0 {
		return fmt.Errorf("the refused call still raised %d permission prompt(s)", n)
	}
	return nil
}

func (s *modelToolsFeatureState) turnEndsWithAnswer() error {
	if s.runErr != nil {
		return fmt.Errorf("turn failed: %v", s.runErr)
	}
	if s.stop != string(acp.StopReasonEndTurn) {
		return fmt.Errorf("stop reason = %q, want end_turn", s.stop)
	}
	msgs := s.st.GetMessages()
	if len(msgs) == 0 {
		return fmt.Errorf("empty transcript")
	}
	if last := msgs[len(msgs)-1]; last.Role != llm.RoleAssistant || !strings.Contains(last.Content, bddModelToolsAnswer) {
		return fmt.Errorf("final answer missing: %+v", last)
	}
	return nil
}

func initializeModelToolsScenario(sc *godog.ScenarioContext) {
	s := &modelToolsFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy session on a model whose tools are "([^"]*)"$`, s.sessionWithTools)
	sc.Step(`^a coddy session on a model whose disallowed tools are "([^"]*)"$`, s.sessionWithDisallowed)
	sc.Step(`^a coddy session on a model with no tool lists$`, s.sessionWithoutLists)
	sc.Step(`^a model that answers directly$`, s.modelAnswersDirectly)
	sc.Step(`^a model that requests the "([^"]+)" tool once, then answers$`, s.modelRequestsToolOnce)
	sc.Step(`^the user sends a message$`, s.userSendsMessage)
	sc.Step(`^the model is offered exactly the tools "([^"]*)"$`, s.offeredExactly)
	sc.Step(`^the system prompt lists exactly the tools "([^"]*)"$`, s.promptListsExactly)
	sc.Step(`^the model is offered the full set of tools without "([^"]*)"$`, s.fullSetWithout)
	sc.Step(`^the system prompt lists the same tools$`, s.promptListsSameTools)
	sc.Step(`^the file is not written$`, s.fileIsNotWritten)
	sc.Step(`^the file is written$`, s.fileIsWritten)
	sc.Step(`^the tool call is answered with the model's tool refusal$`, s.callAnsweredWithModelRefusal)
	sc.Step(`^no permission prompt was raised$`, s.noPermissionPrompt)
	sc.Step(`^the turn ends with the model's answer$`, s.turnEndsWithAnswer)
}

func TestModelToolRestrictionsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "model-tool-restrictions",
		ScenarioInitializer: initializeModelToolsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/model_tool_restrictions.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("model tool restrictions feature suite failed")
	}
}
