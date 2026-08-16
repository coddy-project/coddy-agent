package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ConfigRollbackTool restores the pre-commit snapshot over the active config.
func ConfigRollbackTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "config_rollback",
			Description: "Restore the previous committed configuration from the snapshot written next to the active " +
				"file by config_commit, then hot-reload the runtime. This replaces the current configuration; warn the " +
				"user that changes committed after that snapshot are lost, and get their confirmation before calling.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		RequiresPermission: true,
		Execute:            executeConfigRollback,
	}
}

func executeConfigRollback(ctx context.Context, _ string, env *tooling.Env) (string, error) {
	if env == nil || strings.TrimSpace(env.ConfigPath) == "" {
		return "", fmt.Errorf("active config path is unavailable")
	}
	if env.ReloadConfig == nil {
		return "", fmt.Errorf("runtime config reload is unavailable; config was not changed")
	}
	configStagingMu.Lock()
	defer configStagingMu.Unlock()
	rollback, err := config.RollbackConfigFromSnapshot(toolConfigPaths(env))
	if err != nil {
		return "", err
	}
	warnings, err := env.ReloadConfig(ctx)
	if err != nil {
		undoErr := rollback.Rollback()
		_, restoreErr := env.ReloadConfig(ctx)
		if undoErr != nil || restoreErr != nil {
			return "", fmt.Errorf("reload restored config: %w (undo: %v; restore reload: %v)", err, undoErr, restoreErr)
		}
		return "", fmt.Errorf("reload restored config: %w (rollback undone)", err)
	}
	env.ConfigReloaded = true
	result := map[string]interface{}{
		"ok":          true,
		"config_file": env.ConfigPath,
		"snapshot":    rollback.SnapshotPath,
		"reloaded":    true,
		"warning": fmt.Sprintf("Restored the previous configuration from %s. The replaced configuration was written "+
			"back into the snapshot, so running config_rollback again swaps it back. Changes committed after the "+
			"snapshot are no longer in the active file.", rollback.SnapshotPath),
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return marshalToolResult(result, "config_rollback")
}
