// Package prompts manages system prompt templates for each agent mode.
// Templates are markdown files embedded into the binary and support
// Go text/template syntax for variable substitution.
//
// Template variables available in .md files:
//
//	{{.CWD}}               - session working directory
//	{{.ExtraInstructions}} - extra text from config (prompts.agent_extra / plan_extra)
//	{{if .ExtraInstructions}}...{{end}} - conditional block
package prompts

import (
	_ "embed"
	"fmt"
	"os"
	"strings"
	"text/template"
)

// TemplateData holds values injected into prompt templates.
type TemplateData struct {
	// CWD is the session working directory.
	CWD string

	// ExtraInstructions are appended to the prompt (from config prompts.agent_extra / plan_extra).
	ExtraInstructions string
}

// Embedded default prompt template files.
//
//go:embed agent.md
var defaultAgentPrompt string

//go:embed plan.md
var defaultPlanPrompt string

// Render renders the prompt template for the given mode with the provided data.
// mode must be "agent" or "plan". Unknown modes fall back to "agent".
// customFile overrides the built-in template when non-empty.
func Render(mode, customFile string, data TemplateData) (string, error) {
	src, err := loadSource(mode, customFile)
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
func RenderWithFallback(mode, customFile string, data TemplateData) string {
	s, err := Render(mode, customFile, data)
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

// loadSource returns the template source: custom file if provided, built-in otherwise.
func loadSource(mode, customFile string) (string, error) {
	if customFile != "" {
		data, err := os.ReadFile(customFile)
		if err != nil {
			return "", fmt.Errorf("read custom prompt file %q: %w", customFile, err)
		}
		return string(data), nil
	}
	return DefaultSource(mode), nil
}

func fallbackPrompt(mode, cwd string) string {
	return fmt.Sprintf("You are an AI coding assistant in %s mode.\nWorking directory: %s\n", mode, cwd)
}
