// Package prompts manages system prompt templates for each agent mode.
// Templates are markdown files embedded into the binary or loaded from a directory
// configured as prompts.dir. They use Go text/template for variable substitution.
//
// Template variables available in .md files:
//
//	{{.CWD}}    - session working directory
//	{{.Skills}} - active skills/rules (markdown), built by the agent
//	{{.Tools}}  - readable list of tools available in the current mode (markdown)
//	{{.Memory}} - session agent memory notes (may be empty)
//
// Use {{if .Skills}}...{{end}} (and similarly for .Tools, .Memory) when sections are optional.
package prompts

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

const (
	fileAgent = "agent.md"
	filePlan  = "plan.md"
)

// TemplateData holds values injected into prompt templates.
type TemplateData struct {
	// CWD is the session working directory.
	CWD string

	// Skills is preformatted markdown for active skills and rules (may be empty).
	Skills string

	// Tools is a human-readable markdown list of tools for the current mode (may be empty).
	Tools string

	// Memory is session-scoped notes injected into the prompt (may be empty).
	Memory string
}

// Embedded default prompt template files.
//
//go:embed agent.md
var defaultAgentPrompt string

//go:embed plan.md
var defaultPlanPrompt string

// Render renders the prompt template for the given mode with the provided data.
// promptsDir must be empty to use built-in templates; otherwise it is a directory that
// contains agent.md and/or plan.md (see file names for each mode).
// mode must be "agent" or "plan". Unknown modes use the agent template file.
func Render(mode, promptsDir string, data TemplateData) (string, error) {
	src, err := loadSource(mode, promptsDir)
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
func RenderWithFallback(mode, promptsDir string, data TemplateData) string {
	s, err := Render(mode, promptsDir, data)
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
	default:
		return defaultAgentPrompt
	}
}

func fileNameForMode(mode string) string {
	if mode == "plan" {
		return filePlan
	}
	return fileAgent
}

// loadSource returns the template source: files from promptsDir when set, built-in otherwise.
func loadSource(mode, promptsDir string) (string, error) {
	dir := strings.TrimSpace(promptsDir)
	if dir == "" {
		return DefaultSource(mode), nil
	}

	path := filepath.Join(dir, fileNameForMode(mode))
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt file %q: %w", path, err)
	}
	return string(data), nil
}

func fallbackPrompt(mode, cwd string) string {
	return fmt.Sprintf("You are an AI coding assistant in %s mode.\nWorking directory: %s\n", mode, cwd)
}
