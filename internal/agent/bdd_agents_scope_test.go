package agent

// Godog harness for features/agents_md_scoping.feature: drives the real
// Agent.Run against a fake LLM provider that reads one file and then answers,
// and inspects the system message of every request the provider received. The
// system prompt is what the spec is about, so the provider's view of it is the
// only honest assertion surface.

import (
	"context"
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

const (
	bddRootAgentsToken    = "ROOT_AGENTS_MD_TOKEN"
	bddNestedAgentsToken  = "NESTED_AGENTS_MD_TOKEN"
	bddSiblingAgentsToken = "SIBLING_AGENTS_MD_TOKEN"
)

// bddReadThenAnswerProvider requests a single read on its first call, then answers.
type bddReadThenAnswerProvider struct {
	readPath string
	seen     [][]llm.Message
}

func (p *bddReadThenAnswerProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("Complete must not be used by the AGENTS.md scoping suite")
}

func (p *bddReadThenAnswerProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.seen = append(p.seen, append([]llm.Message(nil), messages...))
	if len(p.seen) == 1 {
		tc := llm.ToolCall{
			ID:        "call_read_1",
			Name:      "read",
			InputJSON: fmt.Sprintf(`{"path":%q}`, p.readPath),
		}
		onChunk(llm.StreamChunk{ToolCall: &tc})
		return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}, nil
	}
	const answer = "Read it; here is the summary."
	onChunk(llm.StreamChunk{TextDelta: answer})
	return &llm.Response{Content: answer, StopReason: "end_turn"}, nil
}

type agentsScopeFeatureState struct {
	tmpDirs  []string
	cwd      string
	st       *session.State
	ag       *Agent
	provider *bddReadThenAnswerProvider
}

func (s *agentsScopeFeatureState) reset() error {
	s.close()
	s.provider = nil
	return nil
}

func (s *agentsScopeFeatureState) close() {
	for _, d := range s.tmpDirs {
		_ = os.RemoveAll(d)
	}
	s.tmpDirs = nil
	s.cwd = ""
	s.st = nil
	s.ag = nil
}

func (s *agentsScopeFeatureState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-agents-scope-*")
	if err != nil {
		return "", err
	}
	s.tmpDirs = append(s.tmpDirs, d)
	return d, nil
}

func (s *agentsScopeFeatureState) projectWithNestedAgentsFiles(nested, sibling string) error {
	cwd, err := s.tempDir()
	if err != nil {
		return err
	}
	s.cwd = cwd
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte(bddRootAgentsToken), 0o644); err != nil {
		return err
	}
	for dir, token := range map[string]string{nested: bddNestedAgentsToken, sibling: bddSiblingAgentsToken} {
		full := filepath.Join(cwd, filepath.FromSlash(dir))
		if err := os.MkdirAll(full, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(full, "AGENTS.md"), []byte(token), 0o644); err != nil {
			return err
		}
	}
	// The file the model will read.
	return os.WriteFile(filepath.Join(cwd, filepath.FromSlash(nested), "react.go"), []byte("package agent\n"), 0o644)
}

func (s *agentsScopeFeatureState) agentSessionInThatProject() error {
	if s.cwd == "" {
		return fmt.Errorf("no project prepared")
	}
	sessionDir, err := s.tempDir()
	if err != nil {
		return err
	}
	s.st = &session.State{
		ID:         "sess_bdd_agents_scope",
		CWD:        s.cwd,
		Mode:       session.ModeAgent,
		SessionDir: sessionDir,
	}
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 6},
	}
	cfg.Prompts.ApplyDefaults()
	s.st.ReplaceRulesCatalog(session.DiscoverRules(cfg, s.cwd))
	s.ag = NewAgent(cfg, s.st, resumePermissionSender{}, nil)
	return nil
}

func (s *agentsScopeFeatureState) modelReadsFileThenAnswers(path string) error {
	if s.ag == nil {
		return fmt.Errorf("no agent prepared")
	}
	s.provider = &bddReadThenAnswerProvider{readPath: path}
	s.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil }
	stop, err := s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "summarize the agent loop"}})
	if err != nil {
		return fmt.Errorf("run failed: %w", err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		return fmt.Errorf("stop reason = %q, want end_turn", stop)
	}
	if len(s.provider.seen) < 2 {
		return fmt.Errorf("expected at least 2 requests (read, then answer), got %d", len(s.provider.seen))
	}
	return nil
}

// systemPrompt returns the system message of the nth request (0-based).
func (s *agentsScopeFeatureState) systemPrompt(n int) (string, error) {
	if n >= len(s.provider.seen) {
		return "", fmt.Errorf("request %d was never made (%d total)", n, len(s.provider.seen))
	}
	msgs := s.provider.seen[n]
	if len(msgs) == 0 || msgs[0].Role != llm.RoleSystem {
		return "", fmt.Errorf("request %d does not start with a system message", n)
	}
	return msgs[0].Content, nil
}

func (s *agentsScopeFeatureState) firstRequestHasRootOnly() error {
	sp, err := s.systemPrompt(0)
	if err != nil {
		return err
	}
	if !strings.Contains(sp, bddRootAgentsToken) {
		return fmt.Errorf("the root AGENTS.md is missing from the first request")
	}
	for _, tok := range []string{bddNestedAgentsToken, bddSiblingAgentsToken} {
		if strings.Contains(sp, tok) {
			return fmt.Errorf("a nested AGENTS.md (%s) was loaded before any tool touched its directory", tok)
		}
	}
	return nil
}

func (s *agentsScopeFeatureState) requestsAfterReadCarryNested(dir string) error {
	for n := 1; n < len(s.provider.seen); n++ {
		sp, err := s.systemPrompt(n)
		if err != nil {
			return err
		}
		if !strings.Contains(sp, bddNestedAgentsToken) {
			return fmt.Errorf("request %d is missing the %q AGENTS.md after the read", n, dir)
		}
		if !strings.Contains(sp, bddRootAgentsToken) {
			return fmt.Errorf("request %d lost the root AGENTS.md", n)
		}
	}
	return nil
}

func (s *agentsScopeFeatureState) noRequestCarriesSibling(dir string) error {
	for n := range s.provider.seen {
		sp, err := s.systemPrompt(n)
		if err != nil {
			return err
		}
		if strings.Contains(sp, bddSiblingAgentsToken) {
			return fmt.Errorf("request %d loaded the untouched %q AGENTS.md", n, dir)
		}
	}
	return nil
}

func initializeAgentsScopeScenario(sc *godog.ScenarioContext) {
	s := &agentsScopeFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a project with a root AGENTS\.md and nested AGENTS\.md files under "([^"]*)" and "([^"]*)"$`, s.projectWithNestedAgentsFiles)
	sc.Step(`^a coddy agent session in that project$`, s.agentSessionInThatProject)
	sc.Step(`^the model reads "([^"]*)" and then answers$`, s.modelReadsFileThenAnswers)
	sc.Step(`^the first request carries the root AGENTS\.md but neither nested one$`, s.firstRequestHasRootOnly)
	sc.Step(`^every request after the read carries the "([^"]*)" AGENTS\.md$`, s.requestsAfterReadCarryNested)
	sc.Step(`^no request carries the "([^"]*)" AGENTS\.md$`, s.noRequestCarriesSibling)
}

func TestAgentsMDScopingFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "agents-md-scoping",
		ScenarioInitializer: initializeAgentsScopeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/agents_md_scoping.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("AGENTS.md scoping feature suite failed")
	}
}
