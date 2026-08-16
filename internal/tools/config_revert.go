package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

type configRevertArgs struct {
	Path string `json:"path"`
}

// ConfigRevertTool discards staged commands without applying them (uci "revert" analog).
func ConfigRevertTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "config_revert",
			Description: "Discard staged configuration commands without applying them. " +
				"With no arguments every pending command is dropped; pass a dotted path to drop only the commands under it.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Optional dotted path; only staged commands addressing it (or below it) are dropped.",
					},
				},
			},
		},
		Execute: executeConfigRevert,
	}
}

func executeConfigRevert(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	if env == nil || strings.TrimSpace(env.ConfigPath) == "" {
		return "", fmt.Errorf("active config path is unavailable")
	}
	configStagingMu.Lock()
	defer configStagingMu.Unlock()
	args, err := tooling.ParseArgs[configRevertArgs](argsJSON)
	if err != nil {
		return "", fmt.Errorf("config_revert parse args: %w", err)
	}
	pending, err := loadStagedConfigCommands(env)
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(args.Path)
	var remaining []string
	// Reverting an already empty staging area is a no-op, with or without a
	// path filter.
	if path != "" && len(pending) > 0 {
		cmds, err := config.ParseUCICommands(pending)
		if err != nil {
			return "", err
		}
		for _, cmd := range cmds {
			if !uciCommandUnderPath(cmd, path) {
				remaining = append(remaining, cmd.String())
			}
		}
	}
	if err := saveStagedConfigCommands(env, remaining); err != nil {
		return "", err
	}
	return marshalToolResult(map[string]interface{}{
		"ok":      true,
		"pending": redactPendingForDisplay(remaining),
	}, "config_revert")
}

// uciCommandUnderPath reports whether the command addresses path or a descendant of it.
func uciCommandUnderPath(cmd config.UCICommand, path string) bool {
	if cmd.Path == path {
		return true
	}
	return strings.HasPrefix(cmd.Path, path+".") || strings.HasPrefix(cmd.Path, path+"[")
}
