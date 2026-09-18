package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ToolSwitchModel is the tool a model calls to change the model or the
// reasoning level it runs on.
const ToolSwitchModel = "switch_model"

// SwitchModelTool lets the model pick its own model and reasoning level from
// the configured ones: a stronger model or deeper reasoning for a hard step, a
// faster or cheaper one for bulk work. The change applies from the next model
// request; it lasts until the turn ends unless the call asks for the session.
// The configured models are listed in the description, so the choice is made
// among what exists.
func SwitchModelTool(cfg *config.Config) *tooling.Tool {
	var models []string
	if cfg != nil {
		for i := range cfg.Models {
			ent := &cfg.Models[i]
			line := "- " + ent.Model
			if levels := cfg.ReasoningChoicesFor(ent); len(levels) > 0 {
				line += " (reasoning: " + strings.Join(levels, ", ") + ")"
			}
			models = append(models, line)
		}
	}
	description := "Switch the model you run on and/or its reasoning level, from your next request. " +
		"Pick a stronger model or a higher reasoning level for a step that needs it (a hard bug, a design decision, a subtle review) " +
		"and a faster one or a lower level for bulk or routine work; say why in your reply. " +
		"By default the change lasts until this turn ends; scope \"session\" keeps it for the conversation, as the user's /model command would, " +
		"so use it only when the user asked for a lasting change. The reasoning level \"off\" turns thinking off where the model offers it, " +
		"\"default\" goes back to the model's own level."
	if len(models) > 0 {
		description += "\n\nConfigured models:\n" + strings.Join(models, "\n")
	}
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        ToolSwitchModel,
			Description: description,
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"model": map[string]interface{}{
						"type":        "string",
						"description": "A configured model id from the list above; omit to keep the current model",
					},
					"reasoning": map[string]interface{}{
						"type":        "string",
						"description": "A reasoning level the chosen model offers, \"off\" or \"default\"; omit to keep the current one",
					},
					"scope": map[string]interface{}{
						"type":        "string",
						"enum":        []interface{}{"turn", "session"},
						"description": "turn (default): until this turn ends; session: for the rest of the conversation",
					},
				},
			},
		},
		RequiresPermission: false,
		Execute:            executeSwitchModel,
	}
}

func executeSwitchModel(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
	if env == nil || env.SwitchModel == nil {
		return "", fmt.Errorf("%s is not available in this session", ToolSwitchModel)
	}
	args, err := tooling.ParseArgs[struct {
		Model     string `json:"model"`
		Reasoning string `json:"reasoning"`
		Scope     string `json:"scope"`
	}](argsJSON)
	if err != nil {
		return "", err
	}
	req := tooling.ModelSwitch{
		Model:     strings.TrimSpace(args.Model),
		Reasoning: strings.TrimSpace(args.Reasoning),
	}
	switch strings.ToLower(strings.TrimSpace(args.Scope)) {
	case "", "turn":
	case "session":
		req.Session = true
	default:
		return "", fmt.Errorf("scope must be turn or session, got %q", args.Scope)
	}
	if req.Model == "" && req.Reasoning == "" {
		return "", fmt.Errorf("name a model, a reasoning level, or both")
	}
	return env.SwitchModel(ctx, req)
}
