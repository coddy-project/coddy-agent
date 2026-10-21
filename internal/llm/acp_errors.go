package llm

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
)

// ACPErrorKind says which part of talking to an ACP agent failed.
type ACPErrorKind string

const (
	// ACPErrorConfig: the row cannot start an agent (no command, a bad proxy,
	// the depth limit) or the session cannot use it (plan or ask mode with no
	// mode of the agent mapped to it).
	ACPErrorConfig ACPErrorKind = "config"
	// ACPErrorUnsupported: the call is not one an acp model serves - a call
	// outside a session (compaction, titles, the goal judge, a direct
	// completion) or a restricted turn.
	ACPErrorUnsupported ACPErrorKind = "unsupported"
	// ACPErrorStart: the agent did not start or did not finish its handshake.
	ACPErrorStart ACPErrorKind = "start"
	// ACPErrorExited: the agent's process ended while a call waited on it.
	ACPErrorExited ACPErrorKind = "exited"
	// ACPErrorRPC: the agent answered a request with an error.
	ACPErrorRPC ACPErrorKind = "rpc"
	// ACPErrorAuth: the agent wants the client to authenticate first.
	ACPErrorAuth ACPErrorKind = "auth"
	// ACPErrorSelect: the model or mode the session asks for is not one the
	// agent offers.
	ACPErrorSelect ACPErrorKind = "select"
	// ACPErrorCancelled: the agent ended the prompt as cancelled while the
	// caller still waited for it.
	ACPErrorCancelled ACPErrorKind = "cancelled"
	// ACPErrorStalled: the prompt sent nothing for the silence bound, or its
	// last outcome is unknown and the call would repeat it.
	ACPErrorStalled ACPErrorKind = "stalled"
)

// ACPError is how the acp provider fails.
type ACPError struct {
	Kind  ACPErrorKind
	Agent string
	// Method is the JSON-RPC method of an rpc or auth error.
	Method  string
	Code    int
	Message string
	Data    string
	Err     error
	// Stderr is the tail of what the agent wrote to stderr, for a process
	// that failed to start or exited.
	Stderr string
	// AuthMethods are the methods the agent offers, for an auth error.
	AuthMethods []string
	// Updated marks a failure after the agent reported anything for the
	// prompt: the call is then never issued again, by the wrapper or by the
	// loop (the streaming rule of every provider, and more: an agent may have
	// run tools without showing text).
	Updated bool
}

func (e *ACPError) Error() string {
	var b strings.Builder
	if e.Agent != "" {
		fmt.Fprintf(&b, "acp agent %s: ", e.Agent)
	} else {
		b.WriteString("acp agent: ")
	}
	switch e.Kind {
	case ACPErrorStart:
		b.WriteString("failed to start")
	case ACPErrorExited:
		b.WriteString("exited")
	case ACPErrorAuth:
		b.WriteString("authentication required")
		if len(e.AuthMethods) > 0 {
			fmt.Fprintf(&b, "; set acp.auth_method to one of: %s, or sign the agent in on its own", strings.Join(e.AuthMethods, ", "))
		} else {
			b.WriteString("; sign the agent in on its own, or set acp.auth_method")
		}
	case ACPErrorRPC:
		if e.Method != "" {
			fmt.Fprintf(&b, "%s failed", e.Method)
		} else {
			b.WriteString("request failed")
		}
	case ACPErrorCancelled:
		b.WriteString("the agent cancelled the prompt")
	}
	msg := e.Message
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if msg != "" {
		switch e.Kind {
		case ACPErrorConfig, ACPErrorSelect, ACPErrorUnsupported, ACPErrorStalled:
			b.WriteString(msg)
		default:
			b.WriteString(": " + msg)
		}
	}
	if e.Data != "" && e.Data != "null" {
		b.WriteString(" (" + strings.Trim(e.Data, `"`) + ")")
	}
	if tail := lastLines(e.Stderr, 6); tail != "" {
		b.WriteString("; stderr: " + tail)
	}
	return b.String()
}

func (e *ACPError) Unwrap() error { return e.Err }

// retryable reports whether a call may be issued again: only when the agent
// never got it far enough to act - a start that failed for a passing reason,
// or a process that ended before reporting anything for the prompt. An agent
// that answered with an error has had its own retries; a missing or
// non-executable program fails the same way every time.
func (e *ACPError) retryable() bool {
	if e.Updated {
		return false
	}
	switch e.Kind {
	case ACPErrorStart:
		// A missing or non-executable program fails the same way again, and
		// a handshake that ran out its time would take that long again.
		return !errors.Is(e.Err, exec.ErrNotFound) && !errors.Is(e.Err, fs.ErrNotExist) && !errors.Is(e.Err, fs.ErrPermission) &&
			!errors.Is(e.Err, context.DeadlineExceeded)
	case ACPErrorExited:
		return true
	}
	return false
}

// withUpdated returns err marked as failing after the agent reported
// something when it did; the shared exit error of a process is copied, never
// changed in place.
func withUpdated(err error, updated bool) error {
	var e *ACPError
	if !updated || !errors.As(err, &e) {
		return err
	}
	c := *e
	c.Updated = true
	return &c
}

// IsACPUnsupported reports whether err is an acp model refusing a call it
// does not serve (outside a session, or a restricted turn).
func IsACPUnsupported(err error) bool {
	var e *ACPError
	return errors.As(err, &e) && e.Kind == ACPErrorUnsupported
}

func lastLines(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
