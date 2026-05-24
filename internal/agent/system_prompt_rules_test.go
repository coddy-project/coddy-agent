package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestBuildSystemPromptIncludesRulesBlock(t *testing.T) {
	tmp := t.TempDir()
	rulePath := filepath.Join(tmp, ".coddy", "rules")
	if err := os.MkdirAll(rulePath, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nalwaysApply: true\nglobs: ['**/*.go']\n---\nRULE_GLOB_TOKEN:xyz\n"
	if err := os.WriteFile(filepath.Join(rulePath, "go.mdc"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	st.ReplaceRulesCatalog(session.DiscoverRules(&config.Config{}, tmp))
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	a := NewAgent(cfg, st, nil, nil)
	prompt := a.buildSystemPrompt("agent", nil, nil, "", []string{filepath.Join(tmp, "main.go")})
	if !strings.Contains(prompt, "RULE_GLOB_TOKEN") {
		t.Fatal("expected rule token in prompt")
	}
	if strings.Contains(prompt, "## Active Rules and Skills") {
		t.Fatal("rule token should be under Rules not Skills heading")
	}
}
