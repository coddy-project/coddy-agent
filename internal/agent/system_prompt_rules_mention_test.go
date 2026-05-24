package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestBuildSystemPromptMentionOnlyRule(t *testing.T) {
	tmp := t.TempDir()
	rulePath := filepath.Join(tmp, ".coddy", "rules")
	if err := os.MkdirAll(rulePath, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nalwaysApply: false\ndescription: mention only\n---\nRULE_MENTION_ONLY:secret\n"
	if err := os.WriteFile(filepath.Join(rulePath, "mention_demo.mdc"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	st.ReplaceRulesCatalog(session.DiscoverRules(&config.Config{}, tmp))
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	a := NewAgent(cfg, st, nil, nil)
	without := a.buildSystemPrompt("agent", nil, nil, "hello", nil)
	if strings.Contains(without, "RULE_MENTION_ONLY") {
		t.Fatal("mention-only rule must not appear without @mention")
	}
	with := a.buildSystemPrompt("agent", nil, nil, "please @mention_demo now", nil)
	if !strings.Contains(with, "RULE_MENTION_ONLY") {
		t.Fatal("expected mention-only rule body with @mention_demo")
	}
}

func TestBuildSystemPromptProjectDocsInRules(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte("AGENTS_DOC_TOKEN"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "DESIGN.md"), []byte("DESIGN_DOC_TOKEN"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	st.ReplaceRulesCatalog(session.DiscoverRules(&config.Config{}, tmp))
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	a := NewAgent(cfg, st, nil, nil)
	prompt := a.buildSystemPrompt("agent", nil, nil, "", nil)
	if !strings.Contains(prompt, "AGENTS_DOC_TOKEN") || !strings.Contains(prompt, "DESIGN_DOC_TOKEN") {
		t.Fatal("expected project docs in rules block")
	}
	agentsIdx := strings.Index(prompt, "AGENTS_DOC_TOKEN")
	designIdx := strings.Index(prompt, "DESIGN_DOC_TOKEN")
	if agentsIdx < 0 || designIdx < 0 || agentsIdx > designIdx {
		t.Fatal("expected AGENTS.md before DESIGN.md in prompt")
	}
}
