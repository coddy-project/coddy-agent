package llm

// BackgroundWake marks the user-role message that opens a turn nobody typed:
// background tasks the model asked to be notified about (notify_on_finish) have
// finished, and the process started a turn to report them. The model reads the
// message's Content like any other user message; the marker is for the
// surfaces, which never show the message as a bubble somebody wrote, live or
// after a reload: the web UI and the console show nothing in its place, an
// editor and a chat a one-line note naming the tasks.
type BackgroundWake struct {
	// Tasks are the tasks the turn reports, in the order they finished.
	Tasks []BackgroundWakeTask `json:"tasks"`
}

// BackgroundWakeTask is one finished task as the woken turn reports it: enough
// to name it and say how it ended without reading the task record.
type BackgroundWakeTask struct {
	// ID is the task id in its session (bg_3).
	ID string `json:"id"`
	// Kind is "command" for a shell command, "agent" for a subagent run.
	Kind string `json:"kind,omitempty"`
	// Label is what the task is: the command, or the run's description.
	Label string `json:"label,omitempty"`
	// Agent names the subagent definition behind an agent run.
	Agent string `json:"agent,omitempty"`
	// Status is the terminal status: succeeded, failed, timed_out, stopped.
	Status string `json:"status"`
	// ExitCode is the process exit code of a command, when it has one.
	ExitCode *int `json:"exit_code,omitempty"`
	// DurationMs is how long the task ran.
	DurationMs int64 `json:"duration_ms"`
	// Error is what went wrong, when the pool recorded something.
	Error string `json:"error,omitempty"`
}
