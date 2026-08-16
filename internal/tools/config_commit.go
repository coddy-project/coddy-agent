package tools

import (
	"context"
	"errors"
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
	// Consume the staged commands before applying them: once the commit is
	// live a leftover staging file must not allow a replay (add_list applied
	// twice), so a consumption failure aborts while nothing has changed, and
	// every failure after this point restores the staged list explicitly.
	if err := saveStagedConfigCommands(env, nil); err != nil {
		return "", fmt.Errorf("consume staged config commands: %w (config was not changed)", err)
	}
	// The combined transaction holds the config file lock across the disk
	// write AND the runtime reload, so no other config writer (another
	// session's commit, the HTTP PUT handler) can interleave between them.
	commit, warnings, err := config.CommitUCICommandsAndReload(toolConfigPaths(env), cmds, func() ([]string, error) {
		return env.ReloadConfig(ctx)
	})
	if err != nil {
		if errors.Is(err, config.ErrConfigStateUncertain) {
			// The active file may still hold (part of) the change; restoring
			// the staged list would let a blind retry replay non-idempotent
			// commands such as add_list.
			return "", fmt.Errorf("%w; staged commands stay consumed to prevent a replay - inspect the config file, then re-stage with config_set", err)
		}
		if restoreErr := saveStagedConfigCommands(env, pending); restoreErr != nil {
			return "", fmt.Errorf("%w (staged commands were lost: %v; stage them again with config_set)", err, restoreErr)
		}
		return "", fmt.Errorf("%w; staged commands kept", err)
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
