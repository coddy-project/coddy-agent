package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ConfigChangesTool lists staged configuration commands (uci "changes" analog).
func ConfigChangesTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "config_changes",
			Description: "List the staged configuration commands that config_commit would apply. " +
				"Use it to review pending edits before asking the user to confirm saving them.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Execute: executeConfigChanges,
	}
}

func executeConfigChanges(_ context.Context, _ string, env *tooling.Env) (string, error) {
	if env == nil || strings.TrimSpace(env.ConfigPath) == "" {
		return "", fmt.Errorf("active config path is unavailable")
	}
	configStagingMu.Lock()
	defer configStagingMu.Unlock()
	pending, err := loadStagedConfigCommands(env)
	if err != nil {
		return "", err
	}
	if pending == nil {
		pending = []string{}
	}
	result := map[string]interface{}{
		"config_file": env.ConfigPath,
		"pending":     pending,
	}
	if len(pending) > 0 {
		result["hint"] = "Nothing is applied until config_commit."
	}
	return marshalToolResult(result, "config_changes")
}
