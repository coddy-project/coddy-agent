package shell

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// RunCommandTool returns the run_command built-in tool.
func RunCommandTool() *tooling.Tool {
	return RunCommandToolForShell(platform.CurrentShell())
}

// RunCommandToolForShell returns run_command bound to a detected shell.
func RunCommandToolForShell(commandShell platform.Shell) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "run_command",
			Description: shellDescription(commandShell),
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
		RequiresPermission: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			return executeRunCommandWithShell(ctx, argsJSON, env, commandShell)
		},
	}
}

func shellDescription(commandShell platform.Shell) string {
	switch commandShell.Kind {
	case platform.ShellPwsh, platform.ShellPowerShell:
		return "Execute a PowerShell command in the working directory. Use native commands such as Get-ChildItem, Select-String, and Get-Process. Returns combined stdout and stderr output."
	case platform.ShellCmd:
		return "Execute a cmd.exe command in the working directory. Use Windows commands such as dir, findstr, and tasklist. Returns combined stdout and stderr output."
	case platform.ShellBash:
		return "Execute a bash command in the working directory using POSIX command syntax. Returns combined stdout and stderr output."
	default:
		return "Execute an sh command in the working directory using POSIX command syntax. Returns combined stdout and stderr output."
	}
}

type runCommandArgs struct {
	Command             string `json:"command"`
	PermissionRationale string `json:"permission_rationale"`
	TimeoutSeconds      int    `json:"timeout_seconds"`
}

func executeRunCommandWithShell(ctx context.Context, argsJSON string, env *tooling.Env, commandShell platform.Shell) (string, error) {
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

	executable, commandArgs := commandShell.Command(args.Command)
	cmd := exec.CommandContext(cmdCtx, executable, commandArgs...)
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
