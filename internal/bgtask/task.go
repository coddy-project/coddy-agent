// Package bgtask runs session-scoped work that outlives a single tool call.
//
// A task is started through the pool, reports progress through snapshots, and
// keeps its captured output available until the session goes away. The pool
// deliberately knows nothing about shells: what a task actually is comes from a
// Runner, so the command runner shipped here can be joined later by other kinds
// of work (a spawned subagent, for instance) without reopening the pool.
package bgtask

import (
	"strings"
	"time"
)

// Status is the lifecycle state of a task.
type Status string

const (
	// StatusQueued means the task was accepted but has not started yet.
	StatusQueued Status = "queued"
	// StatusRunning means the underlying work is in flight.
	StatusRunning Status = "running"
	// StatusSucceeded means the work finished with a zero exit code.
	StatusSucceeded Status = "succeeded"
	// StatusFailed means the work finished with a non-zero exit code or errored.
	StatusFailed Status = "failed"
	// StatusTimedOut means the hard timeout elapsed and the work was terminated.
	StatusTimedOut Status = "timed_out"
	// StatusStopped means an operator or the model terminated the work.
	StatusStopped Status = "stopped"
	// StatusOrphaned means the coddy process restarted while the work was in
	// flight, so the task is known only from what was persisted before the exit.
	StatusOrphaned Status = "orphaned"
)

// Finished reports whether the status is terminal.
func (s Status) Finished() bool {
	switch s {
	case StatusQueued, StatusRunning:
		return false
	default:
		return true
	}
}

// Kind distinguishes what a task runs.
type Kind string

const (
	// KindCommand is a shell command handed to the host interpreter.
	KindCommand Kind = "command"
	// KindAgent is reserved for spawning a nested agent. The pool already
	// carries it end to end so that work lands as a Runner implementation
	// rather than as a second scheduling mechanism.
	KindAgent Kind = "agent"
)

// Spec describes work handed to the pool.
type Spec struct {
	SessionID string
	Kind      Kind
	// Label is the short human-facing name shown in the tasks drawer. The pool
	// derives one from the command when it is empty.
	Label string
	// Command is the shell command for KindCommand.
	Command string
	// CWD is the working directory for the work.
	CWD string
	// ToolCallID links the task back to the transcript row that started it.
	ToolCallID string
	// ExpectedSeconds is the model's own estimate of how long the work takes.
	// It is advisory: it drives the status ticker and the overdue flag, and
	// nothing else. TimeoutSeconds is what actually terminates the work.
	ExpectedSeconds int
	// TimeoutSeconds is the hard limit. Zero asks the pool to derive one.
	TimeoutSeconds int
	// NotifyOnFinish asks the pool to wake the agent when this task reaches a
	// terminal state. It is opt-in per task: the model decides which work is
	// worth an autonomous turn, so a batch of quick commands cannot each start
	// one behind the operator's back.
	NotifyOnFinish bool
}

// Snapshot is an immutable view of a task, safe to hand to callers and to
// serialise for the HTTP surface and the on-disk record.
type Snapshot struct {
	ID              string     `json:"id"`
	SessionID       string     `json:"session_id"`
	Kind            Kind       `json:"kind"`
	Label           string     `json:"label"`
	Command         string     `json:"command,omitempty"`
	CWD             string     `json:"cwd,omitempty"`
	ToolCallID      string     `json:"tool_call_id,omitempty"`
	Status          Status     `json:"status"`
	ExitCode        *int       `json:"exit_code,omitempty"`
	Error           string     `json:"error,omitempty"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	ExpectedSeconds int        `json:"expected_seconds,omitempty"`
	TimeoutSeconds  int        `json:"timeout_seconds"`
	OutputBytes     int64      `json:"output_bytes"`
	OutputTruncated bool       `json:"output_truncated"`
	NotifyOnFinish  bool       `json:"notify_on_finish,omitempty"`

	// PID leads the process group the task runs in. It is persisted so a fresh
	// coddy can tell a record whose processes died with the previous run from
	// one whose processes are still on this machine, and reach the latter.
	PID int `json:"pid,omitempty"`

	// ProcessStartedAt is the exact OS creation time used to prove that a
	// persisted pid still belongs to this task. Persistence writes it to the
	// private meta record; the public HTTP snapshot deliberately omits it.
	ProcessStartedAt time.Time `json:"-"`

	// LastOutputAt is when the task last wrote anything. A task can be running
	// and stuck at the same time; silence is the only signal available without
	// knowing what the command is supposed to do.
	LastOutputAt *time.Time `json:"last_output_at,omitempty"`
}

// SilentFor reports how long the task has produced nothing. It is zero for a
// task that never wrote at all, since there is no silence to measure against.
func (s Snapshot) SilentFor(now time.Time) time.Duration {
	if s.LastOutputAt == nil || s.Status.Finished() {
		return 0
	}
	if now.Before(*s.LastOutputAt) {
		return 0
	}
	return now.Sub(*s.LastOutputAt)
}

// Elapsed is how long the task has been running, or how long it ran in total
// once it finished.
func (s Snapshot) Elapsed(now time.Time) time.Duration {
	if s.StartedAt.IsZero() {
		return 0
	}
	end := now
	if s.FinishedAt != nil {
		end = *s.FinishedAt
	}
	if end.Before(s.StartedAt) {
		return 0
	}
	return end.Sub(s.StartedAt)
}

// Overdue reports whether a still-running task has outlived the model's own
// estimate. An unfinished task without an estimate is never overdue.
func (s Snapshot) Overdue(now time.Time) bool {
	if s.Status.Finished() || s.ExpectedSeconds <= 0 {
		return false
	}
	return s.Elapsed(now) > time.Duration(s.ExpectedSeconds)*time.Second
}

// deriveLabel turns a command into a short drawer label.
func deriveLabel(spec Spec) string {
	if label := strings.TrimSpace(spec.Label); label != "" {
		return label
	}
	command := strings.TrimSpace(spec.Command)
	if command == "" {
		return string(spec.Kind)
	}
	if idx := strings.IndexAny(command, "\r\n"); idx >= 0 {
		command = strings.TrimSpace(command[:idx])
	}
	const maxLabel = 60
	if len(command) > maxLabel {
		return command[:maxLabel-1] + "…"
	}
	return command
}
