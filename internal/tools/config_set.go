package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// configSetHint reminds the model that staging is not saving.
const configSetHint = "Nothing is applied yet. Review with config_changes, confirm with the user, then run config_commit."

type configSetArgs struct {
	Commands []string `json:"commands"`
}

// ConfigSetTool stages UCI-style edits to the active config without applying them.
func ConfigSetTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "config_set",
			Description: "Stage edits to Coddy's active YAML configuration using OpenWrt-uci-like commands: " +
				"\"set <path>=<value>\", \"add_list <path>=<value>\", \"del_list <path>=<value>\", \"delete <path>\". " +
				"Paths are dotted, e.g. agent.max_turns or mcp_servers[name=context7].command; values are JSON or plain scalars. " +
				"Nothing is applied until config_commit: stage, review with config_changes, ask the user to confirm saving, then commit.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"commands": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "UCI-like commands staged in order, e.g. [\"set agent.max_turns=20\"].",
					},
				},
				"required": []interface{}{"commands"},
			},
		},
		Execute: executeConfigSet,
	}
}

func executeConfigSet(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	if env == nil || strings.TrimSpace(env.ConfigPath) == "" {
		return "", fmt.Errorf("active config path is unavailable")
	}
	configStagingMu.Lock()
	defer configStagingMu.Unlock()
	args, err := tooling.ParseArgs[configSetArgs](argsJSON)
	if err != nil {
		return "", fmt.Errorf("config_set parse args: %w", err)
	}
	newCmds, err := config.ParseUCICommands(args.Commands)
	if err != nil {
		return "", err
	}
	staged, err := loadStagedConfigCommands(env)
	if err != nil {
		return "", err
	}
	pending := append([]string(nil), staged...)
	for _, cmd := range newCmds {
		pending = append(pending, cmd.String())
	}
	allCmds, err := config.ParseUCICommands(pending)
	if err != nil {
		return "", err
	}
	// Dry-run the whole pending batch against the file as it is right now, so a
	// broken command is rejected before it is staged.
	if err := config.DryRunUCICommands(toolConfigPaths(env), allCmds); err != nil {
		return "", err
	}
	if err := saveStagedConfigCommands(env, pending); err != nil {
		return "", err
	}
	return marshalToolResult(map[string]interface{}{
		"ok":          true,
		"config_file": env.ConfigPath,
		"pending":     redactPendingForDisplay(pending),
		"hint":        configSetHint,
	}, "config_set")
}

func marshalToolResult(result map[string]interface{}, toolName string) (string, error) {
	// A plain Encoder keeps "<redacted>" placeholders readable in tool output
	// instead of <-escaping them.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(result); err != nil {
		return "", fmt.Errorf("%s encode result: %w", toolName, err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

func toolConfigPaths(env *tooling.Env) config.Paths {
	return config.Paths{Home: env.ConfigHome, CWD: env.ConfigCWD, ConfigPath: env.ConfigPath}
}
