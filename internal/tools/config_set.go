package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

var configSetMu sync.Mutex

type configSetArgs struct {
	Path   string          `json:"path"`
	Value  json.RawMessage `json:"value"`
	Delete bool            `json:"delete"`
}

// ConfigSetTool atomically edits the active config and reloads the runtime.
func ConfigSetTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "config_set",
			Description: "Set or delete one value in Coddy's active YAML configuration, validate and atomically write it, then hot-reload skills, rules, built-in tools, and configured MCP servers. Use selectors such as /mcp_servers[name=context7] and '-' to append to a sequence.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "A non-root slash path in the typed config schema.",
					},
					"value": map[string]interface{}{
						"description": "Any JSON value. Required unless delete is true.",
					},
					"delete": map[string]interface{}{
						"type":        "boolean",
						"description": "Delete the selected mapping key or sequence entry.",
						"default":     false,
					},
				},
				"required": []interface{}{"path"},
			},
		},
		RequiresPermission: true,
		Execute:            executeConfigSet,
	}
}

func executeConfigSet(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
	if env == nil || strings.TrimSpace(env.ConfigPath) == "" {
		return "", fmt.Errorf("active config path is unavailable")
	}
	if env.ReloadConfig == nil {
		return "", fmt.Errorf("runtime config reload is unavailable; config was not changed")
	}
	configSetMu.Lock()
	defer configSetMu.Unlock()
	var args configSetArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("config_set parse args: %w", err)
	}
	edit, err := config.EditConfigPath(toolConfigPaths(env), args.Path, args.Value, args.Delete)
	if err != nil {
		return "", err
	}
	warnings, err := env.ReloadConfig(ctx)
	if err != nil {
		rollbackErr := edit.Rollback()
		_, restoreErr := env.ReloadConfig(ctx)
		if rollbackErr != nil || restoreErr != nil {
			return "", fmt.Errorf("reload updated config: %w (rollback: %v; restore reload: %v)", err, rollbackErr, restoreErr)
		}
		return "", fmt.Errorf("reload updated config: %w (change rolled back)", err)
	}
	env.ConfigReloaded = true
	result := map[string]interface{}{
		"ok":          true,
		"config_file": env.ConfigPath,
		"path":        strings.TrimSpace(args.Path),
		"changed":     edit.Changed,
		"reloaded":    true,
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("config_set encode result: %w", err)
	}
	return string(out), nil
}
