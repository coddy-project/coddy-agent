package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
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

func executeConfigSet(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
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
	// A server the batch adds or changes that runs `npx -y <package>` with no
	// version is pinned to the registry's current release: the pin is one
	// more staged command, so config_changes shows it and config_revert drops
	// it with the rest, and the batch is dry-run again with it in place.
	pinned, pins := pinStagedNPXServers(ctx, toolConfigPaths(env), allCmds)
	if len(pins) > 0 {
		pending = append(pending, pins...)
		if allCmds, err = config.ParseUCICommands(pending); err != nil {
			return "", err
		}
		if err := config.DryRunUCICommands(toolConfigPaths(env), allCmds); err != nil {
			return "", err
		}
	}
	if err := saveStagedConfigCommands(env, pending); err != nil {
		return "", err
	}
	result := map[string]interface{}{
		"ok":          true,
		"config_file": env.ConfigPath,
		"pending":     redactPendingForDisplay(pending),
		"hint":        configSetHint,
	}
	if len(pinned) > 0 {
		result["pinned"] = pinned
		result["hint"] = configSetHint + " " + configSetPinHint
	}
	return marshalToolResult(result, "config_set")
}

// configSetPinHint follows configSetHint when the batch pinned, or failed to
// pin, an npx package.
const configSetPinHint = "Tell the user what `pinned` says - which package was pinned to which version, or why it could not be - before asking them to save."

// pinStagedNPXServers finds the mcp_servers entries the staged batch adds or
// changes and, for each that runs `npx -y <package>` without a version, reads
// the registry's current release. It returns the results for the operator
// and the `set mcp_servers[name=<n>].args=<json>` commands that pin them; a
// package the registry could not resolve gets a result and no command.
func pinStagedNPXServers(ctx context.Context, paths config.Paths, cmds []config.UCICommand) ([]mcp.PinResult, []string) {
	before, after, err := config.StagedMCPServers(paths, cmds)
	if err != nil {
		// The batch already passed its dry run; a document this reader cannot
		// decode is not the batch's fault, and pinning is best effort.
		return nil, nil
	}
	previous := make(map[string]config.MCPServerConfig, len(before))
	for _, srv := range before {
		previous[srv.Name] = srv
	}
	var results []mcp.PinResult
	var pins []string
	for _, srv := range after {
		if prev, ok := previous[srv.Name]; ok && prev.Command == srv.Command && slices.Equal(prev.Args, srv.Args) {
			continue
		}
		args, res := mcp.PinNPXArgs(ctx, mcp.DefaultResolver(), srv.Name, srv.Command, srv.Args)
		if res == nil {
			continue
		}
		results = append(results, *res)
		if args == nil {
			continue
		}
		encoded, err := json.Marshal(args)
		if err != nil {
			continue
		}
		pins = append(pins, "set mcp_servers[name="+srv.Name+"].args="+string(encoded))
	}
	return results, pins
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
