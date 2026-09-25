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
//	{{.ModelSwitch}} - true when the turn is offered switch_model
//	{{.BackgroundWake}} - true when a finished background task can wake this session
//	{{.UTCNow}}   - current date and time in UTC (RFC3339), set each time the system prompt renders
//
// Use {{if .Skills}}...{{end}} (and similarly for .Tools, .Memory, .TodoList) when sections should be omitted when empty.
//
// The ReAct runner renders the system prompt once per turn and then freezes it: the provider caches
// a request by its prefix, and the system message sits in front of the whole conversation, so a byte
// that moves there throws away the cached copy of every message behind it. The built-in templates
// therefore render neither {{.UTCNow}} nor {{.TodoList}}; the runner sends the clock, the checklist
// and the rules a tool call activated after the history, in a <turn_context> block
// (internal/agent/turn_context.go). Both fields stay populated for a template under prompts.dir that
// wants them anyway, at the cost of that cache on every request.
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
	// The built-in templates do not render it: it is rewritten by every
	// coddy_todo_* call and travels in the turn context block instead.
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

	// ModelSwitch reports that the turn is offered switch_model: more than
	// one configured model, or one with reasoning levels, and never a child.
	// The built-in templates describe the tool only then, so a model is not
	// told about a tool it cannot call.
	ModelSwitch bool

	// BackgroundWake reports that a background task this turn starts can wake
	// the session when it finishes: a process with a waker, and a session
	// that is not a subagent or a scheduled run, whose transcript is sealed
	// when the turn returns. The built-in templates promise the wake only
	// then and otherwise tell the model to collect results itself.
	BackgroundWake bool

	// UTCNow is the wall-clock instant in RFC3339 (UTC) at render time for model
	// grounding. The built-in templates do not render it: a clock in the system
	// prompt is a cache miss on every request, and the turn context block carries
	// it after the history.
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

// RenderSource renders a template given as text with the same data a mode
// template gets. A system child with a prompt of its own (the memory
// subagent) renders through it instead of the mode template.
func RenderSource(name, src string, data TemplateData) (string, error) {
	tmpl, err := template.New(name).Parse(src)
	if err != nil {
		return "", fmt.Errorf("parse prompt template %q: %w", name, err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("render prompt template %q: %w", name, err)
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

// RendersRules reports whether the template for mode puts the {{.Rules}} block
// into the prompt at all. The cross-block dedupe depends on it: the project
// docs preamble is dropped from {{.Instructions}} only because the rules block
// carries it, and an operator's own template under prompts.dir is free to
// render one block and not the other. A template that cannot be read falls
// back to the built-in one, which does render the block. The field is looked
// for by name, so {{if .Rules}} and {{ .Rules }} count too: answering yes when
// in doubt keeps the file in one block rather than in none.
func RendersRules(mode, promptsDir, agentFile, planFile, askFile string) bool {
	src, err := loadSource(mode, promptsDir, agentFile, planFile, askFile)
	if err != nil {
		return true
	}
	return strings.Contains(src, ".Rules")
}

// RendersVolatile reports whether the template for mode prints something that
// changes between the steps of one turn: the wall clock, or the todo checklist
// a coddy_todo_* call rewrites. The built-in templates print neither, and the
// ReAct runner can then render the system message once and freeze it. A
// template under prompts.dir that prints either one keeps the old behaviour -
// re-rendered before every LLM call - because its own conditionals around those
// fields have to keep matching the state. It pays for that with the provider's
// prompt cache, which is why the built-in templates stopped doing it.
//
// A source that cannot be read counts as volatile: the render fallback
// (fallbackPrompt) carries a clock of its own.
func RendersVolatile(mode, promptsDir, agentFile, planFile, askFile string) bool {
	src, err := loadSource(mode, promptsDir, agentFile, planFile, askFile)
	if err != nil {
		return true
	}
	return strings.Contains(src, ".UTCNow") || strings.Contains(src, ".TodoList")
}

func fallbackPrompt(mode, cwd string) string {
	return fmt.Sprintf(
		"You are an AI coding assistant in %s mode.\nWorking directory: %s\n\n## Current UTC time\n\n%s\n",
		mode, cwd, time.Now().UTC().Format(time.RFC3339))
}
