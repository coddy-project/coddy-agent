package agent

// Godog harness for features/prompt_cache_prefix.feature: drives the real Agent
// through a scripted provider over a real temp workspace and asserts what the
// request prefix looks like from the provider's side - one frozen system
// message per turn, a history that only ever grows, and every volatile fact
// (clock, checklist, rules a tool call activated) carried in the turn context
// block that trails the history.

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

func (s *pcFeatureState) run() error {
	_, err := s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "go"}})
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

func (s *pcFeatureState) scopedRuleInTurnContextAfterRead() error {
	reqs := s.requests()
	if len(reqs) < 2 {
		return fmt.Errorf("expected at least two requests, got %d", len(reqs))
	}
	block, err := turnContextOf(reqs[1])
	if err != nil {
		return err
	}
	if !strings.Contains(block, pcScopedRuleTag) {
		return fmt.Errorf("turn context block after the read carries no scoped rule: %q", truncateForError(block))
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
	sc.Step(`^the model reads a file, adds a todo item, then answers$`, s.readThenTodoThenAnswer)
	sc.Step(`^the model answers straight away$`, s.answerStraightAway)
	sc.Step(`^the model reads a Go file, then answers$`, s.readGoFileThenAnswer)

	sc.Step(`^every request of that turn carries the same system message$`, s.sameSystemMessageEveryRequest)
	sc.Step(`^every request repeats the previous one up to its turn context block$`, s.requestsGrowByAppendOnly)
	sc.Step(`^the persisted transcript carries no turn context block$`, s.transcriptHasNoTurnContext)
	sc.Step(`^no request carries a wall clock reading in its system message$`, s.noClockInSystemMessage)
	sc.Step(`^the turn context block of the request carries the current UTC time$`, s.turnContextCarriesUTCNow)
	sc.Step(`^no request carries the todo checklist in its system message$`, s.noTodoInSystemMessage)
	sc.Step(`^the turn context block of the last request carries the new todo item$`, s.turnContextCarriesTodo)
	sc.Step(`^the request after the read carries the scoped rule in its turn context block$`, s.scopedRuleInTurnContextAfterRead)
	sc.Step(`^the request after the read carries the system message the turn started with$`, s.systemMessageUnchangedAfterRead)
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
