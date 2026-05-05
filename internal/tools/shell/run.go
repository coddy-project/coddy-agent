package shell

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// RunCommandTool returns the run_command built-in tool.
func RunCommandTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "run_command",
			Description: "Execute a shell command in the working directory. Returns combined stdout and stderr output.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Shell command to execute",
					},
					"permission_rationale": map[string]interface{}{
						"type":        "string",
						"description": "Optional text shown in the permission dialog instead of raw arguments",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Command timeout in seconds (default: 30)",
					},
				},
				"required": []string{"command"},
			},
		},
		AllowedInPlanMode:  false,
		RequiresPermission: true,
		Execute:            executeRunCommand,
	}
}

type runCommandArgs struct {
	Command             string `json:"command"`
	PermissionRationale string `json:"permission_rationale"`
	TimeoutSeconds      int    `json:"timeout_seconds"`
}

func executeRunCommand(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[runCommandArgs](argsJSON)
	if err != nil {
		return "", err
	}

	timeout := 30
	if args.TimeoutSeconds > 0 {
		timeout = args.TimeoutSeconds
	}

	cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", args.Command)
	cmd.Dir = env.CWD

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("command timed out after %d seconds", timeout)
		}
		return fmt.Sprintf("command failed: %v\n%s", err, out.String()), nil
	}

	result := out.String()
	if result == "" {
		return "(no output)", nil
	}
	return result, nil
}
