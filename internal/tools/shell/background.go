package shell

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// Tool names for the background task family, kept in one place so the prompt,
// the registry, and the tests cannot drift apart.
const (
	ToolBackgroundList   = "background_list"
	ToolBackgroundOutput = "background_output"
	ToolBackgroundWait   = "background_wait"
	ToolBackgroundStop   = "background_stop"
	ToolBackgroundReap   = "background_reap"
)

// defaultWaitSeconds bounds background_wait so a model that waits on a
// long-running task cannot stall the turn indefinitely. Hitting it is not an
// error: the tool reports the task as still running and the model can wait again
// or move on.
const defaultWaitSeconds = 60

// maxWaitSeconds is the ceiling for an explicit wait, for the same reason.
const maxWaitSeconds = 300

// defaultOutputTailLines is how much of a task's output the model gets when it
// does not ask for a specific slice.
const defaultOutputTailLines = 100

// stallHintAfter is how long a running task must produce nothing before the
// listing says so. It is a hint, not a verdict: a sleep or a watcher is
// supposed to be silent, so the model decides whether silence means stuck.
const stallHintAfter = 60 * time.Second

// startBackgroundCommand hands a run_command call to the task pool and answers
// with the task id rather than the output.
func startBackgroundCommand(args runCommandArgs, env *tooling.Env) (string, error) {
	pool, err := requirePool(env)
	if err != nil {
		return "", err
	}

	snap, err := pool.Start(bgtask.Spec{
		SessionID:       env.SessionID,
		Kind:            bgtask.KindCommand,
		Command:         args.Command,
		CWD:             env.CWD,
		ToolCallID:      env.ToolCallID,
		ExpectedSeconds: args.ExpectedSeconds,
		TimeoutSeconds:  args.TimeoutSeconds,
		// A subagent's transcript is sealed once its turn returns, so a wake
		// aimed at it would only be refused: child-started tasks never notify.
		NotifyOnFinish: args.NotifyOnFinish && env.SubagentDepth == 0,
	})
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Started background task %s: %s\n", snap.ID, snap.Command)
	if snap.ExpectedSeconds > 0 {
		fmt.Fprintf(&b, "Estimated %s, hard timeout %s.\n", humanSeconds(snap.ExpectedSeconds), humanSeconds(snap.TimeoutSeconds))
	} else {
		fmt.Fprintf(&b, "Hard timeout %s.\n", humanSeconds(snap.TimeoutSeconds))
	}
	if snap.NotifyOnFinish {
		b.WriteString("You will be woken with the outcome when it finishes, so you can end your turn now.")
	} else {
		fmt.Fprintf(&b, "Keep working; check on it with %s or %s, and collect the result with %s.", ToolBackgroundList, ToolBackgroundWait, ToolBackgroundOutput)
	}
	return b.String(), nil
}

// requirePool returns the pool for this session, explaining the refusal when
// background execution is off or unwired.
func requirePool(env *tooling.Env) (*bgtask.Pool, error) {
	if env == nil || env.Background == nil {
		return nil, fmt.Errorf("background tasks are not available in this session")
	}
	if !env.BackgroundEnabled {
		return nil, fmt.Errorf("background tasks are disabled (tools.background.enabled is false); run the command in the foreground instead")
	}
	if strings.TrimSpace(env.SessionDir) != "" {
		env.Background.SetSessionDir(env.SessionID, env.SessionDir)
	}
	return env.Background, nil
}

// BackgroundListTool reports every background task of the session.
func BackgroundListTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolBackgroundList,
			Description: "List the background tasks of this session with their status, elapsed time, and estimate. " +
				"Use it to check on work started with run_command background:true before deciding whether to wait or move on.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Execute: func(_ context.Context, _ string, env *tooling.Env) (string, error) {
			pool, err := requirePool(env)
			if err != nil {
				return "", err
			}
			tasks := pool.List(env.SessionID)
			survivors := pool.Survivors(env.SessionID)
			if len(tasks) == 0 && len(survivors) == 0 {
				return "No background tasks in this session.", nil
			}
			now := time.Now()
			var b strings.Builder
			for _, t := range tasks {
				b.WriteString(formatTaskLine(t, now))
				b.WriteString("\n")
			}
			for _, t := range survivors {
				b.WriteString(formatTaskLine(t, now))
				fmt.Fprintf(&b, " still alive from an earlier run (pid %d), reap with %s", t.PID, ToolBackgroundReap)
				b.WriteString("\n")
			}
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}

// BackgroundOutputTool returns the captured output of one background task.
func BackgroundOutputTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolBackgroundOutput,
			Description: "Return the captured stdout and stderr of a background task. " +
				"Works while the task is still running, so it is also how you follow a build or a watcher.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "string",
						"description": "Task id returned by run_command with background:true",
					},
					"tail_lines": map[string]interface{}{
						"type":        "integer",
						"description": "Return only the last N lines (default 100). Pass 0 for everything retained",
					},
				},
				"required": []string{"task_id"},
			},
		},
		Execute: func(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
			args, err := tooling.ParseArgs[struct {
				TaskID    string `json:"task_id"`
				TailLines *int   `json:"tail_lines"`
			}](argsJSON)
			if err != nil {
				return "", err
			}
			pool, err := requirePool(env)
			if err != nil {
				return "", err
			}

			tail := defaultOutputTailLines
			if args.TailLines != nil {
				tail = *args.TailLines
			}
			text, snap, err := pool.Output(env.SessionID, args.TaskID, tail)
			if err != nil {
				return "", err
			}

			var b strings.Builder
			b.WriteString(formatTaskLine(snap, time.Now()))
			if snap.OutputTruncated {
				b.WriteString("\n(earlier output dropped from the in-memory window; the full log is in the session bundle)")
			}
			b.WriteString("\n\n")
			if strings.TrimSpace(text) == "" {
				b.WriteString("(no output yet)")
			} else {
				b.WriteString(text)
			}
			return b.String(), nil
		},
	}
}

// BackgroundWaitTool blocks for a bounded stretch until a task finishes.
func BackgroundWaitTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolBackgroundWait,
			Description: fmt.Sprintf("Wait for a background task to finish, up to %d seconds by default and %d at most. "+
				"Returning while the task still runs is a normal outcome, not a failure: wait again or do something else.", defaultWaitSeconds, maxWaitSeconds),
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "string",
						"description": "Task id returned by run_command with background:true",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("How long to wait (default %d, maximum %d)", defaultWaitSeconds, maxWaitSeconds),
					},
				},
				"required": []string{"task_id"},
			},
		},
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			args, err := tooling.ParseArgs[struct {
				TaskID         string `json:"task_id"`
				TimeoutSeconds int    `json:"timeout_seconds"`
			}](argsJSON)
			if err != nil {
				return "", err
			}
			pool, err := requirePool(env)
			if err != nil {
				return "", err
			}

			seconds := args.TimeoutSeconds
			if seconds <= 0 {
				seconds = defaultWaitSeconds
			}
			seconds = min(seconds, maxWaitSeconds)

			// The pool honours ctx itself, so a cancelled turn does not leave a
			// goroutine parked here for the rest of the wait.
			snap, waitErr := pool.Wait(ctx, env.SessionID, args.TaskID, time.Duration(seconds)*time.Second)
			if waitErr != nil {
				return "", waitErr
			}

			line := formatTaskLine(snap, time.Now())
			if !snap.Status.Finished() {
				return line + fmt.Sprintf("\nStill running after %ds. Wait again or check %s later.", seconds, ToolBackgroundOutput), nil
			}
			text, _, outErr := pool.Output(env.SessionID, args.TaskID, defaultOutputTailLines)
			if outErr != nil || strings.TrimSpace(text) == "" {
				return line, nil
			}
			return line + "\n\n" + text, nil
		},
	}
}

// BackgroundStopTool terminates a running background task.
func BackgroundStopTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        ToolBackgroundStop,
			Description: "Terminate a running background task and everything it spawned. Stopping a task that already finished changes nothing.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "string",
						"description": "Task id returned by run_command with background:true",
					},
				},
				"required": []string{"task_id"},
			},
		},
		Execute: func(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
			args, err := tooling.ParseArgs[struct {
				TaskID string `json:"task_id"`
			}](argsJSON)
			if err != nil {
				return "", err
			}
			pool, err := requirePool(env)
			if err != nil {
				return "", err
			}
			snap, err := pool.Stop(env.SessionID, args.TaskID)
			if err != nil {
				return "", err
			}
			return formatTaskLine(snap, time.Now()), nil
		},
	}
}

// BackgroundReapTool terminates processes that outlived the coddy run which
// started them.
func BackgroundReapTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolBackgroundReap,
			Description: "Find and kill background processes of this session that outlived the coddy run which started them. " +
				"After a crash or a hard kill, the pool no longer owns those processes but they are still on the machine, and background_stop by id is the only other way to reach them. " +
				"Call it when the task list shows orphaned work, or when something from an earlier run is still holding a port or a lock. " +
				"Reports what it killed; killing nothing is a normal answer.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		RequiresPermission: true,
		Execute: func(_ context.Context, _ string, env *tooling.Env) (string, error) {
			pool, err := requirePool(env)
			if err != nil {
				return "", err
			}

			reaped := pool.ReapSurvivors(env.SessionID)
			if len(reaped) == 0 {
				return "No leftover background processes from an earlier run.", nil
			}

			var b strings.Builder
			fmt.Fprintf(&b, "Killed %d leftover background process group(s):\n", len(reaped))
			for _, t := range reaped {
				fmt.Fprintf(&b, "- %s (pid %d) %s\n", t.ID, t.PID, t.Label)
			}
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}

// formatTaskLine renders one task the way the model reads it: identity, state,
// how long it has taken, and how that compares to what was promised.
func formatTaskLine(t bgtask.Snapshot, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s] %s", t.ID, t.Status, t.Label)

	elapsed := int(t.Elapsed(now).Round(time.Second) / time.Second)
	fmt.Fprintf(&b, " (elapsed %s", humanSeconds(elapsed))
	if t.ExpectedSeconds > 0 {
		fmt.Fprintf(&b, ", estimated %s", humanSeconds(t.ExpectedSeconds))
	}
	if t.ExitCode != nil && t.Status.Finished() {
		fmt.Fprintf(&b, ", exit %d", *t.ExitCode)
	}
	b.WriteString(")")

	if t.Overdue(now) {
		b.WriteString(" overdue")
	}
	if silent := t.SilentFor(now); silent >= stallHintAfter {
		fmt.Fprintf(&b, " silent for %s", humanSeconds(int(silent.Round(time.Second)/time.Second)))
	}
	if t.Error != "" {
		fmt.Fprintf(&b, " error: %s", t.Error)
	}
	return b.String()
}

// humanSeconds renders a duration the way a person would say it.
func humanSeconds(seconds int) string {
	switch {
	case seconds < 0:
		return "0s"
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		if rem := seconds % 60; rem != 0 {
			return fmt.Sprintf("%dm%ds", seconds/60, rem)
		}
		return fmt.Sprintf("%dm", seconds/60)
	default:
		if rem := (seconds % 3600) / 60; rem != 0 {
			return fmt.Sprintf("%dh%dm", seconds/3600, rem)
		}
		return fmt.Sprintf("%dh", seconds/3600)
	}
}
