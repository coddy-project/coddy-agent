package agent

// Godog harness for features/agent_identity.feature: drives a real Agent turn
// with a fake provider and asserts what the system prompt on the wire looks
// like, so the identity line is checked where a gateway would read it rather
// than at the function that builds it.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// The prefix a gateway inspects before giving up on identifying the client.
const bddIdentityWindow = 220

// bddIdentityProvider answers every turn with a canned reply and records the
// message slice it was handed.
type bddIdentityProvider struct {
	seen [][]llm.Message
}

func (p *bddIdentityProvider) Complete(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	p.seen = append(p.seen, messages)
	return &llm.Response{Content: "done", StopReason: "end_turn"}, nil
}

func (p *bddIdentityProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.seen = append(p.seen, messages)
	onChunk(llm.StreamChunk{TextDelta: "done"})
	return &llm.Response{Content: "done", StopReason: "end_turn"}, nil
}

type identityFeatureState struct {
	provider *bddIdentityProvider
	state    *session.State
	ag       *Agent
	tmp      []string
}

func (s *identityFeatureState) reset() error {
	s.provider = &bddIdentityProvider{}
	s.state = nil
	s.ag = nil
	return nil
}

func (s *identityFeatureState) close() {
	for _, d := range s.tmp {
		_ = os.RemoveAll(d)
	}
	s.tmp = nil
}

func (s *identityFeatureState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-identity-")
	if err != nil {
		return "", err
	}
	s.tmp = append(s.tmp, d)
	return d, nil
}

// customPromptsDir writes templates that deliberately never mention Coddy, so
// the scenario proves the line is added rather than inherited from the file.
func (s *identityFeatureState) customPromptsDir() (string, error) {
	dir, err := s.tempDir()
	if err != nil {
		return "", err
	}
	for _, name := range []string{"agent.md", "plan.md"} {
		body := "You are a terse assistant. Working directory: {{.CWD}}\n"
		if err := os.WriteFile(dir+"/"+name, []byte(body), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func (s *identityFeatureState) sessionInMode(mode, templates string) error {
	cwd, err := s.tempDir()
	if err != nil {
		return err
	}
	sessionDir, err := s.tempDir()
	if err != nil {
		return err
	}

	sessionMode := session.ModeAgent
	if mode == "plan" {
		sessionMode = session.ModePlan
	}
	s.state = &session.State{
		ID:         "sess_bdd_identity",
		CWD:        cwd,
		Mode:       sessionMode,
		SessionDir: sessionDir,
	}

	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()

	switch templates {
	case "built-in":
		// Embedded templates; nothing to configure.
	case "custom":
		dir, err := s.customPromptsDir()
		if err != nil {
			return err
		}
		cfg.Prompts.Dir = dir
	default:
		return fmt.Errorf("unknown template source %q", templates)
	}

	s.ag = NewAgent(cfg, s.state, resumePermissionSender{}, nil)
	s.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil }
	return nil
}

func (s *identityFeatureState) sendsTurn() error {
	_, err := s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "probe prompt"}})
	return err
}

func (s *identityFeatureState) systemPrompt() (string, error) {
	if len(s.provider.seen) == 0 {
		return "", fmt.Errorf("provider received no request")
	}
	msgs := s.provider.seen[len(s.provider.seen)-1]
	if len(msgs) == 0 || msgs[0].Role != llm.RoleSystem {
		return "", fmt.Errorf("first message is not a system prompt: %+v", msgs)
	}
	return msgs[0].Content, nil
}

func (s *identityFeatureState) namesCoddyInOpening() error {
	prompt, err := s.systemPrompt()
	if err != nil {
		return err
	}
	head := prompt
	if len(head) > bddIdentityWindow {
		head = head[:bddIdentityWindow]
	}
	if !strings.Contains(strings.ToLower(head), "you are coddy") {
		return fmt.Errorf("opening does not name Coddy:\n%s", head)
	}
	return nil
}

func (s *identityFeatureState) namesCoddyOnce() error {
	prompt, err := s.systemPrompt()
	if err != nil {
		return err
	}
	if n := strings.Count(strings.ToLower(prompt), "you are coddy"); n != 1 {
		return fmt.Errorf("system prompt names Coddy %d times, want 1", n)
	}
	return nil
}

func initializeIdentityScenario(sc *godog.ScenarioContext) {
	s := &identityFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a session in "([^"]+)" mode using "([^"]+)" prompt templates$`, s.sessionInMode)
	sc.Step(`^the agent sends a turn to the model$`, s.sendsTurn)
	sc.Step(`^the system prompt of that request names Coddy in its opening$`, s.namesCoddyInOpening)
	sc.Step(`^the system prompt names Coddy exactly once$`, s.namesCoddyOnce)
}

func TestAgentIdentityFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "agent-identity",
		ScenarioInitializer: initializeIdentityScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/agent_identity.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("agent identity feature suite failed")
	}
}
