package agent

// Godog harness for features/model_tuned_prompts.feature: drives a real Agent
// turn with a fake provider and asserts on the system prompt that reaches the
// wire, so the model-tuned sections are checked where the model reads them.

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

// bddPromptProvider answers every turn with a canned reply and records the
// message slice it was handed.
type bddPromptProvider struct {
	seen [][]llm.Message
}

func (p *bddPromptProvider) Complete(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	p.seen = append(p.seen, messages)
	return &llm.Response{Content: "done", StopReason: "end_turn"}, nil
}

func (p *bddPromptProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.seen = append(p.seen, messages)
	onChunk(llm.StreamChunk{TextDelta: "done"})
	return &llm.Response{Content: "done", StopReason: "end_turn"}, nil
}

type modelPromptsFeatureState struct {
	provider *bddPromptProvider
	cfg      *config.Config
	state    *session.State
	tmp      []string
}

func (s *modelPromptsFeatureState) reset() {
	s.provider = &bddPromptProvider{}
	s.cfg = nil
	s.state = nil
}

func (s *modelPromptsFeatureState) close() {
	for _, d := range s.tmp {
		_ = os.RemoveAll(d)
	}
	s.tmp = nil
}

func (s *modelPromptsFeatureState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-model-prompts-")
	if err != nil {
		return "", err
	}
	s.tmp = append(s.tmp, d)
	return d, nil
}

func (s *modelPromptsFeatureState) sessionOnModel(modelRef, providerType string) error {
	cwd, err := s.tempDir()
	if err != nil {
		return err
	}
	sessionDir, err := s.tempDir()
	if err != nil {
		return err
	}
	provider, _, _ := strings.Cut(modelRef, "/")
	s.cfg = &config.Config{
		Providers: []config.ProviderConfig{{Name: provider, Type: providerType, APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: modelRef, MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: modelRef},
	}
	s.cfg.Agent.ApplyDefaults()
	s.cfg.Prompts.ApplyDefaults()
	s.state = &session.State{ID: "sess_bdd_model_prompts", CWD: cwd, Mode: session.ModeAgent, SessionDir: sessionDir}
	return nil
}

func (s *modelPromptsFeatureState) perProviderOff() error {
	off := false
	s.cfg.Prompts.PerProvider.Enabled = &off
	return nil
}

func (s *modelPromptsFeatureState) promptsDirHolds(a, b, c string) error {
	dir, err := s.tempDir()
	if err != nil {
		return err
	}
	for _, name := range []string{a, b, c} {
		body := "Template " + name + ". Working directory: {{.CWD}}\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	s.cfg.Prompts.Dir = dir
	return nil
}

func (s *modelPromptsFeatureState) sendsTurn() error {
	ag := NewAgent(s.cfg, s.state, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil }
	_, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "probe prompt"}})
	return err
}

func (s *modelPromptsFeatureState) systemPrompt() (string, error) {
	if len(s.provider.seen) == 0 {
		return "", fmt.Errorf("provider received no request")
	}
	msgs := s.provider.seen[len(s.provider.seen)-1]
	if len(msgs) == 0 || msgs[0].Role != llm.RoleSystem {
		return "", fmt.Errorf("first message is not a system prompt: %+v", msgs)
	}
	return msgs[0].Content, nil
}

func (s *modelPromptsFeatureState) carriesNotes(family string) error {
	prompt, err := s.systemPrompt()
	if err != nil {
		return err
	}
	if !strings.Contains(prompt, "### Model-family notes ("+family) {
		return fmt.Errorf("system prompt lacks the %q model-family notes:\n%s", family, prompt)
	}
	return nil
}

func (s *modelPromptsFeatureState) carriesNoNotes() error {
	prompt, err := s.systemPrompt()
	if err != nil {
		return err
	}
	if strings.Contains(prompt, "Model-family notes") {
		return fmt.Errorf("system prompt carries model-family notes although they are switched off")
	}
	return nil
}

func (s *modelPromptsFeatureState) contains(text string) error {
	prompt, err := s.systemPrompt()
	if err != nil {
		return err
	}
	if !strings.Contains(prompt, text) {
		return fmt.Errorf("system prompt lacks %q", text)
	}
	return nil
}

func (s *modelPromptsFeatureState) builtFrom(name string) error {
	return s.contains("Template " + name + ".")
}

func (s *modelPromptsFeatureState) namesCoddyOnce() error {
	prompt, err := s.systemPrompt()
	if err != nil {
		return err
	}
	if n := strings.Count(strings.ToLower(prompt), "you are coddy"); n != 1 {
		return fmt.Errorf("system prompt names Coddy %d times, want 1", n)
	}
	return nil
}

func initializeModelPromptsScenario(sc *godog.ScenarioContext) {
	s := &modelPromptsFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a session on the model "([^"]+)" served by a "([^"]+)" provider$`, s.sessionOnModel)
	sc.Step(`^prompts\.per_provider\.enable is false$`, s.perProviderOff)
	sc.Step(`^prompts\.dir holds "([^"]+)", "([^"]+)" and "([^"]+)"$`, s.promptsDirHolds)
	sc.Step(`^the agent sends a turn to the model$`, s.sendsTurn)
	sc.Step(`^the system prompt of that request carries the "([^"]+)" model-family notes$`, s.carriesNotes)
	sc.Step(`^the system prompt of that request carries no model-family notes$`, s.carriesNoNotes)
	sc.Step(`^the system prompt of that request contains "([^"]+)"$`, s.contains)
	sc.Step(`^the system prompt of that request is built from "([^"]+)"$`, s.builtFrom)
	sc.Step(`^the system prompt names Coddy exactly once$`, s.namesCoddyOnce)
}

func TestModelTunedPromptsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "model-tuned-prompts",
		ScenarioInitializer: initializeModelPromptsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/model_tuned_prompts.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("model-tuned prompts feature suite failed")
	}
}
