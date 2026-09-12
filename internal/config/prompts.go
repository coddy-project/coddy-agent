package config

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultAgentPromptFile = "agent.md"
	defaultPlanPromptFile  = "plan.md"
	defaultAskPromptFile   = "ask.md"
)

// Prompts is the YAML prompts section (key prompts).
type Prompts struct {
	Dir         string             `yaml:"dir" json:"dir"`
	AgentPrompt string             `yaml:"agent_prompt"`
	PlanPrompt  string             `yaml:"plan_prompt"`
	AskPrompt   string             `yaml:"ask_prompt"`
	PerProvider PerProviderPrompts `yaml:"per_provider"`
}

// PerProviderPrompts selects the system prompt variant tuned to the active model
// (model_notes and notes fragments of the built-in prompts, <mode>.<slug>.md and
// <mode>.<family>.md files under prompts.dir), falling back to the shared prompt
// when no variant exists.
type PerProviderPrompts struct {
	// Enabled is a pointer so an unset value defaults to true while an explicit
	// false is preserved. Use PerProviderEnabled to read the effective value.
	Enabled *bool `yaml:"enable"`
}

// PerProviderEnabled reports whether model-tuned prompt selection is active.
// Unset (nil) defaults to true.
func (c *Prompts) PerProviderEnabled() bool {
	return c.PerProvider.Enabled == nil || *c.PerProvider.Enabled
}

// ApplyDefaults sets agent_prompt, plan_prompt, and ask_prompt when empty and trims when set.
// per_provider.enable stays nil when omitted, so a saved config does not grow the key.
func (c *Prompts) ApplyDefaults() {
	if strings.TrimSpace(c.AgentPrompt) == "" {
		c.AgentPrompt = defaultAgentPromptFile
	} else {
		c.AgentPrompt = strings.TrimSpace(c.AgentPrompt)
	}
	if strings.TrimSpace(c.PlanPrompt) == "" {
		c.PlanPrompt = defaultPlanPromptFile
	} else {
		c.PlanPrompt = strings.TrimSpace(c.PlanPrompt)
	}
	if strings.TrimSpace(c.AskPrompt) == "" {
		c.AskPrompt = defaultAskPromptFile
	} else {
		c.AskPrompt = strings.TrimSpace(c.AskPrompt)
	}
}

// AgentFile returns the template file name for agent mode (under prompts.dir).
func (c *Prompts) AgentFile() string {
	if s := strings.TrimSpace(c.AgentPrompt); s != "" {
		return s
	}
	return defaultAgentPromptFile
}

// PlanFile returns the template file name for plan mode (under prompts.dir).
func (c *Prompts) PlanFile() string {
	if s := strings.TrimSpace(c.PlanPrompt); s != "" {
		return s
	}
	return defaultPlanPromptFile
}

// AskFile returns the template file name for ask mode (under prompts.dir).
func (c *Prompts) AskFile() string {
	if s := strings.TrimSpace(c.AskPrompt); s != "" {
		return s
	}
	return defaultAskPromptFile
}

// Validate normalises the prompts section in place.
func (c *Prompts) Validate() error {
	c.Dir = strings.TrimSpace(c.Dir)
	c.ApplyDefaults()
	return nil
}

// ResolvedDir returns the prompts directory with ~ and ${CWD} expanded for session cwd.
func (c *Prompts) ResolvedDir(sessionCWD string) string {
	d := strings.TrimSpace(c.Dir)
	if d == "" {
		return ""
	}
	return filepath.Clean(expandPromptsCWD(d, sessionCWD))
}

func expandPromptsCWD(s, cwd string) string {
	s = strings.ReplaceAll(s, "${CWD}", cwd)
	return expandPromptsHome(s)
}

func expandPromptsHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}
