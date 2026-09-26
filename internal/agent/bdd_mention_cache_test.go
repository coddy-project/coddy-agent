package agent

// Godog harness for features/prompt_cache_mentions.feature. Two real turns of
// Agent.Run against a provider that records every request; the prompt of each
// turn is resolved the way the manager resolves it
// (session.Manager.ResolvePromptMentions). The provider's view is the only
// honest one: the scenario compares the system message of every request and
// the bytes the second turn replays for the first turn's message.

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
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

type mentionCacheFeatureState struct {
	dir      string
	st       *session.State
	ag       *Agent
	provider *bddAnswerProvider
	// turns holds the requests of each turn in order.
	turns [][][]llm.Message
}

func (s *mentionCacheFeatureState) close() {
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
	*s = mentionCacheFeatureState{}
}

func (s *mentionCacheFeatureState) project(file, goRule, ruleName, mentionRule, skillName, skillBody string) error {
	dir, err := os.MkdirTemp("", "coddy-bdd-mention-cache-*")
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	s.dir = dir
	rulesDir := filepath.Join(dir, ".coddy", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		return err
	}
	writes := map[string]string{
		filepath.Join(dir, file):                "package main\n",
		filepath.Join(rulesDir, "go.mdc"):        "---\nglobs: **/*.go\nalwaysApply: false\n---\n" + goRule + "\n",
		filepath.Join(rulesDir, ruleName+".mdc"): "---\nalwaysApply: false\ndescription: manual\n---\n" + mentionRule + "\n",
	}
	for p, body := range writes {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return err
		}
	}
	s.st = &session.State{ID: "sess_bdd_mention_cache", CWD: dir, Mode: session.ModeAgent}
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 4},
	}
	cfg.Prompts.ApplyDefaults()
	s.st.ReplaceRulesCatalog(session.DiscoverRules(cfg, dir))
	s.st.ReplaceSkills([]*skills.Skill{{
		Name:        "SKILL",
		FilePath:    filepath.Join(dir, "skills", skillName, "SKILL.md"),
		Description: "release notes",
		Content:     skillBody,
	}})
	s.ag = NewAgent(cfg, s.st, resumePermissionSender{}, nil)
	s.provider = &bddAnswerProvider{}
	s.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil }
	return nil
}

func (s *mentionCacheFeatureState) userSends(text string) error {
	before := len(s.provider.seen)
	blocks := (*session.Manager)(nil).ResolvePromptMentions(context.Background(), s.st,
		[]acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}}, session.MentionScope{})
	stop, err := s.ag.Run(context.Background(), blocks)
	if err != nil {
		return err
	}
	if stop != string(acp.StopReasonEndTurn) {
		return fmt.Errorf("stop reason %q", stop)
	}
	s.turns = append(s.turns, s.provider.seen[before:])
	return nil
}

// firstTurnMessage is the user message the first turn sent, as the model read it.
func (s *mentionCacheFeatureState) firstTurnMessage(requests [][]llm.Message) (string, error) {
	for _, msgs := range requests {
		for _, m := range msgs {
			if m.Role == llm.RoleUser && strings.HasPrefix(m.Content, "review @main.go") {
				return m.Content, nil
			}
		}
	}
	return "", fmt.Errorf("the first turn's message was not sent")
}

func (s *mentionCacheFeatureState) firstMessageCarries(tail string) error {
	if len(s.turns) == 0 {
		return fmt.Errorf("no turn ran")
	}
	msg, err := s.firstTurnMessage(s.turns[0])
	if err != nil {
		return err
	}
	for _, tok := range quotedTokens(tail) {
		if !strings.Contains(msg, tok) {
			return fmt.Errorf("the first turn's message is missing %s:\n%s", tok, msg)
		}
	}
	return nil
}

func (s *mentionCacheFeatureState) sameSystemMessage() error {
	var first string
	n := 0
	for ti, turn := range s.turns {
		for ri, msgs := range turn {
			if len(msgs) == 0 || msgs[0].Role != llm.RoleSystem {
				return fmt.Errorf("turn %d request %d does not open with a system message", ti+1, ri+1)
			}
			if n == 0 {
				first = msgs[0].Content
			} else if msgs[0].Content != first {
				return fmt.Errorf("turn %d request %d opens with another system message:\n--- first\n%s\n--- this one\n%s", ti+1, ri+1, first, msgs[0].Content)
			}
			n++
		}
	}
	if n < 2 {
		return fmt.Errorf("expected requests from both turns, got %d", n)
	}
	for _, tok := range []string{"GO_RULE_TOKEN", "DEPLOY_RULE_TOKEN", "RELEASE_SKILL_TOKEN"} {
		if strings.Contains(first, tok) {
			return fmt.Errorf("the system message carries %s", tok)
		}
	}
	return nil
}

func (s *mentionCacheFeatureState) replayedUnchanged() error {
	if len(s.turns) < 2 {
		return fmt.Errorf("expected two turns, got %d", len(s.turns))
	}
	sent, err := s.firstTurnMessage(s.turns[0])
	if err != nil {
		return err
	}
	replayed, err := s.firstTurnMessage(s.turns[1])
	if err != nil {
		return err
	}
	if replayed != sent {
		return fmt.Errorf("the second turn replayed another first message:\n--- sent\n%s\n--- replayed\n%s", sent, replayed)
	}
	return nil
}

func initializeMentionCacheScenario(sc *godog.ScenarioContext) {
	s := &mentionCacheFeatureState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a project with a file "([^"]*)", a rule for Go files reading "([^"]*)", a mention-only rule "([^"]*)" reading "([^"]*)" and a skill "([^"]*)" reading "([^"]*)"$`, s.project)
	sc.Step(`^the user sends "([^"]*)" and the model answers$`, s.userSends)
	sc.Step(`^the first turn's message carries ("[^"]+"(?:(?:,| and) "[^"]+")*)$`, s.firstMessageCarries)
	sc.Step(`^every request of both turns opens with the same system message$`, s.sameSystemMessage)
	sc.Step(`^the second turn replays the first turn's message unchanged$`, s.replayedUnchanged)
}

func TestPromptCacheMentionsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "mention-prompt-cache",
		ScenarioInitializer: initializeMentionCacheScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/prompt_cache_mentions.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("mention prompt cache feature suite failed")
	}
}
