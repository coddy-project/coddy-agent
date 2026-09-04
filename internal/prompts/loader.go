// Package prompts manages system prompt templates for each agent mode.
// Templates are markdown files embedded into the binary or loaded from a directory
// configured under YAML key prompts (config.Prompts in internal/config/prompts.go). They use Go text/template for variable substitution.
//
// Template variables available in .md files:
//
//	{{.CWD}}      - session working directory
//	{{.Tools}}    - readable list of tools available in the current mode (markdown)
//	{{.Skills}}   - active skills markdown (slash catalog and bodies), built by the agent
//	{{.Memory}}   - session agent memory notes (may be empty)
//	{{.TodoList}} - current session todo checklist rendered as markdown (empty until plan tools populate state)
//	{{.Subagents}} - catalog of subagents the session may spawn (may be empty)
//	{{.SubagentRole}} - role block when this session is itself a subagent run (may be empty)
//	{{.UTCNow}}   - current date and time in UTC (RFC3339), set each time the system prompt renders
//
// Use {{if .Skills}}...{{end}} (and similarly for .Tools, .Memory, .TodoList) when sections should be omitted when empty.
// The ReAct runner refreshes the rendered system prompt before each LLM call while handling one session prompt.
package prompts

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

const (
	fileAgent = "agent.md"
	filePlan  = "plan.md"
	fileAsk   = "ask.md"
)

// TemplateData holds values injected into prompt templates.
type TemplateData struct {
	// CWD is the session working directory.
	CWD string

	// Skills is preformatted markdown for slash skills (may be empty).
	Skills string

	// Rules is preformatted markdown for active project rules (may be empty).
	Rules string

	// Tools is a human-readable markdown list of tools for the current mode (may be empty).
	Tools string

	// Memory is session-scoped notes injected into the prompt (may be empty).
	Memory string

	// TodoList is the current session checklist as markdown lines (may be empty).
	TodoList string

	// PlanContext is design plan text injected when the user runs a saved plan (may be empty).
	PlanContext string

	// DiscardedPlans is plan-mode guidance when the user discarded design plan slugs (may be empty).
	DiscardedPlans string

	// Instructions is the concatenated content of project instruction files (AGENTS.md etc.), may be empty.
	Instructions string

	// Subagents is the catalog block a parent that may spawn subagents reads (may be empty).
	Subagents string

	// SubagentRole is the role block of a child agent run (may be empty).
	SubagentRole string

	// UTCNow is the wall-clock instant in RFC3339 (UTC) at render time for model grounding.
	UTCNow string
}

// Embedded default prompt template files.
//
//go:embed agent.md
var defaultAgentPrompt string

//go:embed plan.md
var defaultPlanPrompt string

//go:embed ask.md
var defaultAskPrompt string

// Render renders the prompt template for the given mode with the provided data.
// promptsDir must be empty to use built-in templates; otherwise it is a directory that
// contains the files named agentFile, planFile, and askFile (for example agent.md,
// plan.md, and ask.md). mode must be "agent", "plan", or "ask". Unknown modes use the
// agent template file.
func Render(mode, promptsDir, agentFile, planFile, askFile string, data TemplateData) (string, error) {
	src, err := loadSource(mode, promptsDir, agentFile, planFile, askFile)
	if err != nil {
		return "", err
	}

	tmpl, err := template.New(mode).Parse(src)
	if err != nil {
		return "", fmt.Errorf("parse prompt template %q: %w", mode, err)
	}

	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("render prompt template %q: %w", mode, err)
	}

	return strings.TrimSpace(b.String()), nil
}

// RenderWithFallback renders the prompt and returns a safe default on error.
func RenderWithFallback(mode, promptsDir, agentFile, planFile, askFile string, data TemplateData) string {
	s, err := Render(mode, promptsDir, agentFile, planFile, askFile, data)
	if err != nil {
		return fallbackPrompt(mode, data.CWD)
	}
	return s
}

// DefaultSource returns the built-in template source for a mode.
// Useful for displaying to the user so they can customize it.
func DefaultSource(mode string) string {
	switch mode {
	case "plan":
		return defaultPlanPrompt
	case "ask":
		return defaultAskPrompt
	default:
		return defaultAgentPrompt
	}
}

func fileNameForMode(mode string) string {
	switch mode {
	case "plan":
		return filePlan
	case "ask":
		return fileAsk
	default:
		return fileAgent
	}
}

// loadSource returns the template source: files from promptsDir when set, built-in otherwise.
func loadSource(mode, promptsDir, agentFile, planFile, askFile string) (string, error) {
	dir := strings.TrimSpace(promptsDir)
	if dir == "" {
		return DefaultSource(mode), nil
	}

	fn := strings.TrimSpace(agentFile)
	switch mode {
	case "plan":
		fn = strings.TrimSpace(planFile)
	case "ask":
		fn = strings.TrimSpace(askFile)
	}
	if fn == "" {
		fn = fileNameForMode(mode)
	}

	path := filepath.Join(dir, fn)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt file %q: %w", path, err)
	}
	return string(data), nil
}

func fallbackPrompt(mode, cwd string) string {
	return fmt.Sprintf(
		"You are an AI coding assistant in %s mode.\nWorking directory: %s\n\n## Current UTC time\n\n%s\n",
		mode, cwd, time.Now().UTC().Format(time.RFC3339))
}
