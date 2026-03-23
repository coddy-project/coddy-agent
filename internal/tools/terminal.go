package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// runCommandTool returns the run_command built-in tool.
func runCommandTool() *Tool {
	return &Tool{
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
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func executeRunCommand(ctx context.Context, argsJSON string, env *Env) (string, error) {
	args, err := parseArgs[runCommandArgs](argsJSON)
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
		// Return exit error with output so LLM can see the failure.
		return fmt.Sprintf("command failed: %v\n%s", err, out.String()), nil
	}

	result := out.String()
	if result == "" {
		return "(no output)", nil
	}
	return result, nil
}
