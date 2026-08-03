package shell

import (
	"context"
	"fmt"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
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
						"type": "integer",
						"description": "Command timeout in seconds. Foreground default 30. A foreground command that outlives it is not killed: it is handed to the background task pool and the tool answers with the new task id plus the output so far, " +
							"so never re-run the same command with a larger timeout_seconds - the first copy is still running. A background task derives its limit from expected_seconds when this is omitted",
					},
					"background": map[string]interface{}{
						"type": "boolean",
						"description": "Run the command as a background task and return a task id immediately instead of waiting for output. " +
							"Use it for work that takes longer than a few seconds (builds, test suites, installs, watchers, servers, batch downloads) so you can keep working while it runs. " +
							"Collect the result later with background_list, background_output, and background_wait; terminate with background_stop",
					},
					"notify_on_finish": map[string]interface{}{
						"type": "boolean",
						"description": "Wake yourself when this background task finishes: a new turn starts automatically with the outcome, even if nobody sends a message. " +
							"Use it for work whose result you must act on (a build, a migration, a long test run) so the session can continue unattended. " +
							"Leave it off for chores you will simply read later with background_list, otherwise every one of them costs a separate turn",
					},
					"expected_seconds": map[string]interface{}{
						"type": "integer",
						"description": "Your own estimate of how long a background command needs. It drives the status ticker the user sees and, when timeout_seconds is omitted, the hard timeout. " +
							"Estimate honestly: too low only marks the task overdue, it does not kill it",
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
		return "Execute a PowerShell command in the working directory. Use native commands such as Get-ChildItem, Select-String, and Get-Process. " +
			"Multi-line commands are supported. To pass text literally, single-quote it ('...') or use a here-string (@'...'@): inside double quotes PowerShell treats the backtick as an escape character, " +
			"so a value containing backticks, or Markdown such as `code` or **bold**, is altered or fails to parse. Bash heredocs (<< 'EOF') are not PowerShell syntax; write the text to a file and pass the path instead. " +
			"Returns combined stdout and stderr output."
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
	Background          bool   `json:"background"`
	ExpectedSeconds     int    `json:"expected_seconds"`
	NotifyOnFinish      bool   `json:"notify_on_finish"`
}

func executeRunCommandWithShell(ctx context.Context, argsJSON string, env *tooling.Env, commandShell platform.Shell) (string, error) {
	args, err := tooling.ParseArgs[runCommandArgs](argsJSON)
	if err != nil {
		return "", err
	}

	if args.Background {
		return startBackgroundCommand(args, env)
	}

	timeout := defaultForegroundTimeoutSeconds
	if args.TimeoutSeconds > 0 {
		timeout = args.TimeoutSeconds
	}

	sc, err := startForeground(args.Command, env, commandShell)
	if err != nil {
		return fmt.Sprintf("command failed: %v", err), nil
	}

	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()

	select {
	case <-sc.waited:
		return sc.completedResult(), nil

	case <-ctx.Done():
		if sc.finished() {
			return sc.completedResult(), nil
		}
		return sc.terminate(cancelledNotice(sc)), nil

	case <-timer.C:
		// A command can exit in the same instant the timer fires, and select
		// picks at random among ready cases: ask before declaring it long-lived.
		if sc.finished() {
			return sc.completedResult(), nil
		}
		adopted, adoptErr := adoptForeground(sc, args, env)
		if adopted != nil {
			return joinNotice(adoptionNotice(adopted.snap, timeout), adopted.prefix), nil
		}
		return sc.terminate(terminationNotice(timeout, adoptErr)), nil
	}
}

// joinNotice keeps the notice ahead of the output. The tool output ceiling
// truncates from the end, so a notice placed after a long log is the first
// thing the model loses.
func joinNotice(notice, output string) string {
	if output == "" {
		return notice
	}
	return notice + "\n\n" + output
}

// adoptionNotice tells the model that nothing was cancelled, names the task it
// now owns, and closes the door on the retry loop a bare timeout used to invite.
func adoptionNotice(snap bgtask.Snapshot, timeout int) string {
	return fmt.Sprintf(
		"Command still running after %s. It was NOT cancelled: it now runs as background task %s (hard timeout %s).\n"+
			"Follow it with %s task_id=%q, %s, or terminate it with %s.\n"+
			"Do NOT run this command again with a larger timeout_seconds: it is already running, and a second copy would fight the first one for the same ports, locks and files.\n"+
			"Output captured so far:",
		humanSeconds(timeout), snap.ID, humanSeconds(snap.TimeoutSeconds),
		ToolBackgroundOutput, snap.ID, ToolBackgroundWait, ToolBackgroundStop,
	)
}

// terminationNotice explains the one case where a long-lived command really is
// killed: nothing was available to take ownership of it.
func terminationNotice(timeout int, reason error) string {
	because := ""
	if reason != nil {
		because = fmt.Sprintf(" (could not hand it over: %v)", reason)
	}
	return fmt.Sprintf(
		"Command exceeded its %s foreground timeout and its whole process group was terminated%s.\n"+
			"Re-run it with background: true and an honest expected_seconds estimate, not with a larger timeout_seconds.\n"+
			"Output captured before the timeout:",
		humanSeconds(timeout), because,
	)
}

// cancelledNotice covers a turn the operator stopped.
func cancelledNotice(sc *startedCommand) string {
	elapsed := int(time.Since(sc.startedAt).Round(time.Second) / time.Second)
	return fmt.Sprintf(
		"Command cancelled after %s; its whole process group was terminated.\nOutput captured before the cancellation:",
		humanSeconds(elapsed),
	)
}
