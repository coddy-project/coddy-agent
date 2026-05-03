package config

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultAgentPromptFile = "agent.md"
	defaultPlanPromptFile  = "plan.md"
)

// Prompts is the YAML prompts section (key prompts).
type Prompts struct {
	Dir         string `yaml:"dir" json:"dir"`
	AgentPrompt string `yaml:"agent_prompt"`
	PlanPrompt  string `yaml:"plan_prompt"`
}

// ApplyDefaults sets agent_prompt and plan_prompt when empty and trims when set.
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
