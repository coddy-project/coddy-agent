package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

type configGetArgs struct {
	Path string `json:"path"`
}

// ConfigGetTool returns a redacted value from the active config.yaml.
func ConfigGetTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "config_get",
			Description: "Read a redacted value from Coddy's active YAML configuration using a dotted uci-style path. Select sequence entries with field[key=value]. Use \".\" for the whole file.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Dotted path such as skills, agent.model, skills.dirs.0, or mcp_servers[name=context7].",
					},
				},
				"required": []interface{}{"path"},
			},
		},
		Execute: executeConfigGet,
	}
}

func executeConfigGet(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	if env == nil || strings.TrimSpace(env.ConfigPath) == "" {
		return "", fmt.Errorf("active config path is unavailable")
	}
	var args configGetArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("config_get parse args: %w", err)
	}
	value, err := config.ReadConfigPath(toolConfigPaths(env), args.Path)
	if err != nil {
		return "", err
	}
	result := map[string]interface{}{
		"config_file": env.ConfigPath,
		"path":        strings.TrimSpace(args.Path),
		"exists":      value.Exists,
	}
	if value.Exists {
		result["value"] = value.Value
	}
	if value.Redacted {
		result["redacted"] = true
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("config_get encode result: %w", err)
	}
	return string(out), nil
}
