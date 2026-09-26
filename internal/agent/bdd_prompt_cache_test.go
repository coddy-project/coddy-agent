package agent

// Godog harness for features/prompt_cache_prefix.feature: drives the real Agent
// through a scripted provider over a real temp workspace and asserts what the
// request prefix looks like from the provider's side - one frozen system
// message per turn, a history that only ever grows, every volatile fact (clock,
// checklist) carried in the turn context block that trails the history, and a
// rule scoped to paths carried in the result of the call that first matched it.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// pcStep is one scripted assistant step: tool calls to execute, or a final
// answer when calls is empty.
type pcStep struct {
	calls []llm.ToolCall
	text  string
}

type pcScriptProvider struct {
	steps []pcStep
	i     int
	seen  [][]llm.Message
}

func (p *pcScriptProvider) Complete(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	return &llm.Response{Content: "summary", StopReason: "end_turn"}, nil
}

func (p *pcScriptProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.seen = append(p.seen, append([]llm.Message(nil), messages...))
	var step pcStep
	if p.i < len(p.steps) {
		step = p.steps[p.i]
	} else {
		step = pcStep{text: "done"}
	}
	p.i++
	if len(step.calls) > 0 {
		return &llm.Response{ToolCalls: step.calls, StopReason: "tool_use"}, nil
	}
	if step.text == "" {
		step.text = "done"
	}
	onChunk(llm.StreamChunk{TextDelta: step.text})
	return &llm.Response{Content: step.text, StopReason: "end_turn"}, nil
}

const (
	pcTodoItem      = "PCACHE_TODO_ITEM"
	pcScopedRuleTag = "PCACHE_SCOPED_GO_RULE"
)

// rfc3339Clock matches a wall clock reading down to the second, the shape that
// used to sit in the middle of the system prompt.
var rfc3339Clock = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`)

type pcFeatureState struct {
	tmpDirs    []string
	cwd        string
	sessionDir string
	st         *session.State
	ag         *Agent
	provider   *pcScriptProvider
}

func (s *pcFeatureState) reset() error {
	s.close()
	s.provider = &pcScriptProvider{}
	var err error
	if s.cwd, err = s.tempDir(); err != nil {
		return err
	}
	store, err := s.tempDir()
	if err != nil {
		return err
	}
	s.sessionDir = filepath.Join(store, "bundle")
	return os.MkdirAll(s.sessionDir, 0o755)
}

func (s *pcFeatureState) close() {
	for _, d := range s.tmpDirs {
		_ = os.RemoveAll(d)
	}
	s.tmpDirs = nil
	s.st = nil
	s.ag = nil
}

func (s *pcFeatureState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-pcache-*")
	if err != nil {
		return "", err
	}
	s.tmpDirs = append(s.tmpDirs, d)
	return d, nil
}

func (s *pcFeatureState) buildAgent() {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "fake/model"},
		Tools:     config.Tools{PermissionMode: config.PermModeBypass},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	s.st = &session.State{ID: "sess_bdd_pcache", CWD: s.cwd, Mode: session.ModeAgent, SessionDir: s.sessionDir}
	s.st.ReplaceRulesCatalog(session.DiscoverRules(cfg, s.cwd))
	s.ag = NewAgent(cfg, s.st, resumePermissionSender{}, nil)
	s.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil }
}

func (s *pcFeatureState) session() error {
	if err := s.reset(); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.cwd, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		return err
	}
	s.buildAgent()
	return nil
}

func (s *pcFeatureState) sessionWithScopedGoRule() error {
	if err := s.reset(); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.cwd, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		return err
	}
	dir := filepath.Join(s.cwd, ".coddy", "rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := "---\ndescription: Go files\nglobs: **/*.go\nalwaysApply: false\n---\n\n" + pcScopedRuleTag + "\n"
	if err := os.WriteFile(filepath.Join(dir, "gofiles.mdc"), []byte(body), 0o644); err != nil {
		return err
	}
	s.buildAgent()
	return nil
}

// sessionWithAgentsMD opens a session in a workspace whose AGENTS.md holds text.
func (s *pcFeatureState) sessionWithAgentsMD(text string) error {
	if err := s.reset(); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.cwd, "AGENTS.md"), []byte(text+"\n"), 0o644); err != nil {
		return err
	}
	s.buildAgent()
	return nil
}

func (s *pcFeatureState) rewriteAgentsMD(text string) error {
	return os.WriteFile(filepath.Join(s.cwd, "AGENTS.md"), []byte(text+"\n"), 0o644)
}

func (s *pcFeatureState) run() error {
	return s.runPrompt("go")
}

// runPrompt runs one turn of the session on the steps scripted for it.
func (s *pcFeatureState) runPrompt(text string) error {
	s.provider.i = 0
	_, err := s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: text}})
	return err
}

func pcReadCall(id, path string) llm.ToolCall {
	b, _ := json.Marshal(map[string]interface{}{"path": path})
	return llm.ToolCall{ID: id, Name: "read", InputJSON: string(b)}
}

func pcTodoAddCall(id, content string) llm.ToolCall {
	b, _ := json.Marshal(map[string]interface{}{"content": content})
	return llm.ToolCall{ID: id, Name: "coddy_todo_item_add", InputJSON: string(b)}
}

func (s *pcFeatureState) readThenTodoThenAnswer() error {
	s.provider.steps = []pcStep{
		{calls: []llm.ToolCall{pcReadCall("r1", "main.go")}},
		{calls: []llm.ToolCall{pcTodoAddCall("t1", pcTodoItem)}},
		{text: "answer"},
	}
	return s.run()
}

func (s *pcFeatureState) answerStraightAway() error {
	s.provider.steps = []pcStep{{text: "answer"}}
	return s.run()
}

func (s *pcFeatureState) readGoFileThenAnswer() error {
	s.provider.steps = []pcStep{
		{calls: []llm.ToolCall{pcReadCall("r1", "main.go")}},
		{text: "answer"},
	}
	return s.run()
}

// askAgainReadingTheSameGoFile is a second turn that reads main.go again, as
// call r2.
func (s *pcFeatureState) askAgainReadingTheSameGoFile() error {
	s.provider.steps = []pcStep{
		{calls: []llm.ToolCall{pcReadCall("r2", "main.go")}},
		{text: "answer again"},
	}
	return s.runPrompt("look at it once more")
}

func (s *pcFeatureState) askAgainAnswerStraightAway() error {
	s.provider.steps = []pcStep{{text: "answer again"}}
	return s.runPrompt("and now?")
}

// operatorCompacts runs the /compact command, which folds the whole
// conversation into a summary the scripted provider writes.
func (s *pcFeatureState) operatorCompacts() error {
	_, err := s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "/compact"}})
	if err != nil {
		return err
	}
	for _, m := range s.st.GetMessages() {
		if m.CompactionSummary {
			return nil
		}
	}
	return fmt.Errorf("the /compact command left no summary in the transcript")
}

func (s *pcFeatureState) requests() [][]llm.Message {
	return s.provider.seen
}

// turnContextOf returns the trailing turn context block of a request.
func turnContextOf(req []llm.Message) (string, error) {
	if len(req) == 0 {
		return "", fmt.Errorf("empty request")
	}
	last := req[len(req)-1]
	if !strings.Contains(last.Content, turnContextOpenTag) {
		return "", fmt.Errorf("last message is not a turn context block: role=%s content=%q", last.Role, truncateForError(last.Content))
	}
	return last.Content, nil
}

func truncateForError(s string) string {
	if len(s) <= 300 {
		return s
	}
	return s[:300] + "…"
}

func (s *pcFeatureState) sameSystemMessageEveryRequest() error {
	reqs := s.requests()
	if len(reqs) < 2 {
		return fmt.Errorf("expected at least two requests, got %d", len(reqs))
	}
	first := reqs[0][0]
	if first.Role != llm.RoleSystem {
		return fmt.Errorf("first message is %s, not system", first.Role)
	}
	for i, r := range reqs[1:] {
		if r[0].Role != llm.RoleSystem {
			return fmt.Errorf("request %d does not start with a system message", i+1)
		}
		if r[0].Content != first.Content {
			return fmt.Errorf("system message changed between request 0 and request %d", i+1)
		}
	}
	return nil
}

func (s *pcFeatureState) requestsGrowByAppendOnly() error {
	reqs := s.requests()
	if len(reqs) < 2 {
		return fmt.Errorf("expected at least two requests, got %d", len(reqs))
	}
	for i := 1; i < len(reqs); i++ {
		prev := reqs[i-1]
		if len(prev) == 0 {
			return fmt.Errorf("request %d is empty", i-1)
		}
		// Everything but the trailing turn context block must reappear verbatim.
		head := prev[:len(prev)-1]
		cur := reqs[i]
		if len(cur) < len(head) {
			return fmt.Errorf("request %d is shorter than the history of request %d", i, i-1)
		}
		for j := range head {
			if !reflect.DeepEqual(head[j], cur[j]) {
				return fmt.Errorf("message %d changed between request %d and request %d:\nbefore: %q\nafter:  %q",
					j, i-1, i, truncateForError(head[j].Content), truncateForError(cur[j].Content))
			}
		}
	}
	return nil
}

func (s *pcFeatureState) noClockInSystemMessage() error {
	for i, r := range s.requests() {
		if loc := rfc3339Clock.FindString(r[0].Content); loc != "" {
			return fmt.Errorf("request %d carries a wall clock reading %q in its system message", i, loc)
		}
	}
	return nil
}

func (s *pcFeatureState) turnContextCarriesUTCNow() error {
	reqs := s.requests()
	if len(reqs) == 0 {
		return fmt.Errorf("no requests recorded")
	}
	block, err := turnContextOf(reqs[0])
	if err != nil {
		return err
	}
	stamp := rfc3339Clock.FindString(block)
	if stamp == "" {
		return fmt.Errorf("turn context block carries no UTC time: %q", truncateForError(block))
	}
	if !strings.HasPrefix(stamp, time.Now().UTC().Format("2006-01-02")) {
		return fmt.Errorf("turn context clock %q is not today in UTC", stamp)
	}
	return nil
}

func (s *pcFeatureState) noTodoInSystemMessage() error {
	for i, r := range s.requests() {
		if strings.Contains(r[0].Content, pcTodoItem) {
			return fmt.Errorf("request %d carries the todo checklist in its system message", i)
		}
	}
	return nil
}

func (s *pcFeatureState) turnContextCarriesTodo() error {
	reqs := s.requests()
	if len(reqs) == 0 {
		return fmt.Errorf("no requests recorded")
	}
	block, err := turnContextOf(reqs[len(reqs)-1])
	if err != nil {
		return err
	}
	if !strings.Contains(block, pcTodoItem) {
		return fmt.Errorf("turn context block of the last request carries no todo item: %q", truncateForError(block))
	}
	return nil
}

// toolResultOf returns the content of the result of call id in req, the way
// the provider was sent it.
func toolResultOf(req []llm.Message, id string) (string, bool) {
	for _, m := range req {
		if m.Role == llm.RoleTool && m.ToolCallID == id {
			return m.Content, true
		}
	}
	return "", false
}

func (s *pcFeatureState) scopedRuleInReadResult() error {
	reqs := s.requests()
	if len(reqs) < 2 {
		return fmt.Errorf("expected at least two requests, got %d", len(reqs))
	}
	res, ok := toolResultOf(reqs[1], "r1")
	if !ok {
		return fmt.Errorf("the request after the read carries no result of it")
	}
	if !strings.Contains(res, pcScopedRuleTag) {
		return fmt.Errorf("the result of the read carries no scoped rule: %q", truncateForError(res))
	}
	return nil
}

func (s *pcFeatureState) noTurnContextCarriesScopedRule() error {
	for i, r := range s.requests() {
		block, err := turnContextOf(r)
		if err != nil {
			return fmt.Errorf("request %d: %w", i, err)
		}
		if strings.Contains(block, pcScopedRuleTag) {
			return fmt.Errorf("the turn context block of request %d carries the scoped rule", i)
		}
	}
	return nil
}

// onlyFirstReadCarriesScopedRule looks at the last request, which replays both
// reads: the rule rides in the first one's result and in nothing else.
func (s *pcFeatureState) onlyFirstReadCarriesScopedRule() error {
	reqs := s.requests()
	if len(reqs) == 0 {
		return fmt.Errorf("no requests recorded")
	}
	last := reqs[len(reqs)-1]
	first, ok := toolResultOf(last, "r1")
	if !ok {
		return fmt.Errorf("the last request does not replay the first read")
	}
	if !strings.Contains(first, pcScopedRuleTag) {
		return fmt.Errorf("the first read's result lost the scoped rule: %q", truncateForError(first))
	}
	second, ok := toolResultOf(last, "r2")
	if !ok {
		return fmt.Errorf("the last request does not carry the second read")
	}
	if strings.Contains(second, pcScopedRuleTag) {
		return fmt.Errorf("the second read's result repeats the scoped rule: %q", truncateForError(second))
	}
	var all strings.Builder
	for _, m := range last {
		all.WriteString(m.Content)
	}
	if n := strings.Count(all.String(), pcScopedRuleTag); n != 1 {
		return fmt.Errorf("the last request carries the scoped rule %d times, want once", n)
	}
	return nil
}

func (s *pcFeatureState) secondReadCarriesScopedRuleAgain() error {
	reqs := s.requests()
	if len(reqs) == 0 {
		return fmt.Errorf("no requests recorded")
	}
	last := reqs[len(reqs)-1]
	if _, ok := toolResultOf(last, "r1"); ok {
		return fmt.Errorf("the compaction left the first read in the request")
	}
	second, ok := toolResultOf(last, "r2")
	if !ok {
		return fmt.Errorf("the last request does not carry the second read")
	}
	if !strings.Contains(second, pcScopedRuleTag) {
		return fmt.Errorf("the second read's result carries no scoped rule after the compaction: %q", truncateForError(second))
	}
	return nil
}

func (s *pcFeatureState) systemMessageCarries(text string) error {
	reqs := s.requests()
	if len(reqs) == 0 {
		return fmt.Errorf("no requests recorded")
	}
	if !strings.Contains(reqs[0][0].Content, text) {
		return fmt.Errorf("the system message does not carry %q", text)
	}
	return nil
}

func (s *pcFeatureState) systemMessageUnchangedAfterRead() error {
	reqs := s.requests()
	if len(reqs) < 2 {
		return fmt.Errorf("expected at least two requests, got %d", len(reqs))
	}
	if reqs[1][0].Content != reqs[0][0].Content {
		return fmt.Errorf("the scoped rule rewrote the system message between request 0 and request 1")
	}
	return nil
}

// The block is runtime state for one request, not something the user said. It
// must reach the provider and nothing else.
func (s *pcFeatureState) transcriptHasNoTurnContext() error {
	for i, m := range s.st.GetMessages() {
		if strings.Contains(m.Content, turnContextOpenTag) {
			return fmt.Errorf("transcript message %d (%s) carries the turn context block", i, m.Role)
		}
	}
	return nil
}

func initializePromptCacheScenario(sc *godog.ScenarioContext) {
	s := &pcFeatureState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return ctx, err
	})

	sc.Step(`^an agent session in a workspace$`, s.session)
	sc.Step(`^an agent session in a workspace holding a rule scoped to Go files$`, s.sessionWithScopedGoRule)
	sc.Step(`^an agent session in a workspace whose AGENTS\.md says "([^"]*)"$`, s.sessionWithAgentsMD)
	sc.Step(`^the model reads a file, adds a todo item, then answers$`, s.readThenTodoThenAnswer)
	sc.Step(`^the model answers straight away$`, s.answerStraightAway)
	sc.Step(`^the model reads a Go file, then answers$`, s.readGoFileThenAnswer)
	sc.Step(`^the user asks again, and the model reads the same Go file, then answers$`, s.askAgainReadingTheSameGoFile)
	sc.Step(`^the user asks again, and the model answers straight away$`, s.askAgainAnswerStraightAway)
	sc.Step(`^the operator compacts the conversation$`, s.operatorCompacts)
	sc.Step(`^the workspace AGENTS\.md is rewritten to say "([^"]*)"$`, s.rewriteAgentsMD)

	sc.Step(`^every request of that turn carries the same system message$`, s.sameSystemMessageEveryRequest)
	sc.Step(`^every request repeats the previous one up to its turn context block$`, s.requestsGrowByAppendOnly)
	sc.Step(`^the persisted transcript carries no turn context block$`, s.transcriptHasNoTurnContext)
	sc.Step(`^no request carries a wall clock reading in its system message$`, s.noClockInSystemMessage)
	sc.Step(`^the turn context block of the request carries the current UTC time$`, s.turnContextCarriesUTCNow)
	sc.Step(`^no request carries the todo checklist in its system message$`, s.noTodoInSystemMessage)
	sc.Step(`^the turn context block of the last request carries the new todo item$`, s.turnContextCarriesTodo)
	sc.Step(`^the result of the read carries the scoped rule$`, s.scopedRuleInReadResult)
	sc.Step(`^no turn context block carries the scoped rule$`, s.noTurnContextCarriesScopedRule)
	sc.Step(`^the request after the read carries the system message the turn started with$`, s.systemMessageUnchangedAfterRead)
	sc.Step(`^every request of both turns opens with the same system message$`, s.sameSystemMessageEveryRequest)
	sc.Step(`^only the first read's result carries the scoped rule$`, s.onlyFirstReadCarriesScopedRule)
	sc.Step(`^the second read's result carries the scoped rule again$`, s.secondReadCarriesScopedRuleAgain)
	sc.Step(`^that system message carries "([^"]*)"$`, s.systemMessageCarries)
}

func TestPromptCachePrefixFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "prompt-cache-prefix",
		ScenarioInitializer: initializePromptCacheScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/prompt_cache_prefix.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("prompt cache prefix feature suite failed")
	}
}
