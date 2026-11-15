package agent

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// The instructions the client that opened the session asked for land in the
// system prompt, under their own heading, after the agent's own text: ACP's
// append mode adds to the agent's instructions and never replaces them.
func TestBuildSystemPromptCarriesTheClientsInstructions(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	st := &session.State{ID: "t", CWD: t.TempDir(), Mode: session.ModeAgent}
	st.SetACPSystemPromptWithoutPersist("You are a code review specialist. Focus on security issues.")
	a := NewAgent(cfg, st, nil, nil)

	prompt := a.buildSystemPrompt("agent", nil, nil)
	if !strings.Contains(prompt, "## Client system instructions") {
		t.Fatalf("the prompt carries no client block:\n%s", prompt)
	}
	if !strings.Contains(prompt, "You are a code review specialist. Focus on security issues.") {
		t.Fatalf("the prompt does not carry the client's text:\n%s", prompt)
	}
	// Append, not override: the agent's own instructions are still there.
	if !strings.Contains(strings.ToLower(prompt), "coddy") {
		t.Fatalf("the prompt lost the agent's own instructions:\n%s", prompt)
	}
}

// A session no client gave instructions to carries no block at all.
func TestBuildSystemPromptHasNoClientBlockByDefault(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	st := &session.State{ID: "t", CWD: t.TempDir(), Mode: session.ModeAgent}
	a := NewAgent(cfg, st, nil, nil)

	if prompt := a.buildSystemPrompt("agent", nil, nil); strings.Contains(prompt, "## Client system instructions") {
		t.Fatalf("a session without a client prompt carries one:\n%s", prompt)
	}
}
