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

// SwitchModelTool changes the model or reasoning level when the user requests
// it. The configured models are listed so the requested choice can be checked.
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
	description := "Switch the model you run on and/or its reasoning level from your next request, only when the user asks in this conversation. " +
		"Do not switch on your own for a difficult step or for ordinary work. " +
		"By default the user's choice lasts for the session, like /model. Use scope \"turn\" only when the user limited the request to this turn or task. " +
		"The reasoning level \"off\" turns thinking off where the model offers it, " +
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
						"description": "A configured model id from the list above, " + tooling.ModelChoiceRule + "; omit to keep the current model",
					},
					"reasoning": map[string]interface{}{
						"type":        "string",
						"description": "A reasoning level the chosen model offers, \"off\" or \"default\", " + tooling.ModelChoiceRule + "; omit to keep the current one",
					},
					"scope": map[string]interface{}{
						"type":        "string",
						"enum":        []interface{}{"turn", "session"},
						"description": "session (default): for the rest of the conversation; turn: only for this turn or task when the user asks",
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
	case "", "session":
		req.Session = true
	case "turn":
	default:
		return "", fmt.Errorf("scope must be turn or session, got %q", args.Scope)
	}
	if req.Model == "" && req.Reasoning == "" {
		return "", fmt.Errorf("name a model, a reasoning level, or both")
	}
	return env.SwitchModel(ctx, req)
}
