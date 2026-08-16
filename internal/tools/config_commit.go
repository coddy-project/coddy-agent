package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ConfigCommitTool applies the staged commands to the active config and hot-reloads the runtime.
func ConfigCommitTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "config_commit",
			Description: "Apply the staged configuration commands: validate the batch, snapshot the previous file " +
				"next to the config, write atomically, then hot-reload skills, rules, built-in tools, and configured " +
				"MCP servers. Call it only after the user confirmed the staged changes should be saved.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		RequiresPermission: true,
		Execute:            executeConfigCommit,
	}
}

func executeConfigCommit(ctx context.Context, _ string, env *tooling.Env) (string, error) {
	if env == nil || strings.TrimSpace(env.ConfigPath) == "" {
		return "", fmt.Errorf("active config path is unavailable")
	}
	if env.ReloadConfig == nil {
		return "", fmt.Errorf("runtime config reload is unavailable; config was not changed")
	}
	configStagingMu.Lock()
	defer configStagingMu.Unlock()
	pending, err := loadStagedConfigCommands(env)
	if err != nil {
		return "", err
	}
	if len(pending) == 0 {
		return "", fmt.Errorf("no staged config commands; stage edits with config_set first")
	}
	cmds, err := config.ParseUCICommands(pending)
	if err != nil {
		return "", err
	}
	commit, err := config.CommitUCICommands(toolConfigPaths(env), cmds)
	if err != nil {
		return "", err
	}
	warnings, err := env.ReloadConfig(ctx)
	if err != nil {
		rollbackErr := commit.Rollback()
		_, restoreErr := env.ReloadConfig(ctx)
		if rollbackErr != nil || restoreErr != nil {
			return "", fmt.Errorf("reload committed config: %w (rollback: %v; restore reload: %v)", err, rollbackErr, restoreErr)
		}
		return "", fmt.Errorf("reload committed config: %w (change rolled back; staged commands kept)", err)
	}
	if err := saveStagedConfigCommands(env, nil); err != nil {
		return "", err
	}
	env.ConfigReloaded = true
	result := map[string]interface{}{
		"ok":          true,
		"config_file": env.ConfigPath,
		"applied":     commit.Applied,
		"changed":     commit.Changed,
		"reloaded":    true,
		"snapshot":    commit.SnapshotPath,
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return marshalToolResult(result, "config_commit")
}
