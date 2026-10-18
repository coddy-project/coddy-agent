package acp

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// The answers a surface gives to an operator command. None of them carries
// the command: the tool runs the command it holds, so what the operator was
// shown is what runs, whatever a client sends.
const (
	OperatorActionRun     = "run"
	OperatorActionDecline = "decline"
	OperatorActionStop    = "stop"
)

// The states of one operator command request.
const (
	OperatorStateWaiting  = "waiting"
	OperatorStateRunning  = "running"
	OperatorStateStopping = "stopping"
	OperatorStateKilling  = "killing"
	OperatorStateDeclined = "declined"
	OperatorStateEnded    = "ended"
)

var (
	// ErrOperatorCommandConflict is an answer that does not fit the state of
	// the request: a second run, a stop before run, anything after a decline.
	ErrOperatorCommandConflict = errors.New("operator command: the answer does not fit the state of the request")
	// ErrOperatorCommandEnded is an answer to a request that is over.
	ErrOperatorCommandEnded = errors.New("operator command: the request has ended")
	// ErrOperatorCommandAction is an answer that is none of run, decline, stop.
	ErrOperatorCommandAction = errors.New("operator command: the action must be run, decline or stop")
)

// OperatorCommandRequest is what a surface shows the operator: the command
// exactly as it will run, the shell, the host and the folder it runs in, and
// the agent's reason for asking.
type OperatorCommandRequest struct {
	SessionID  string `json:"sessionId"`
	RequestID  string `json:"requestId"`
	ToolCallID string `json:"toolCallId"`
	Command    string `json:"command"`
	Reason     string `json:"reason"`
	CWD        string `json:"cwd"`
	// Shell is the kind of the operator shell (bash, pwsh, zsh, ...) and
	// ShellPath the executable it resolved to.
	Shell     string `json:"shell"`
	ShellPath string `json:"shellPath"`
	// Host is the machine the command runs on: the one that owns the session.
	Host string `json:"host"`
	// Multiplexer is "tmux" or "screen" when the command runs inside one, and
	// AttachCommand how the operator reaches its terminal there.
	Multiplexer   string `json:"multiplexer,omitempty"`
	AttachCommand string `json:"attachCommand,omitempty"`
}

// OperatorCommandDecision is the operator's answer. After a run, Stop closes
// on the first stop the operator sends and Kill on the second; the tool calls
// Done once the command has ended, which ends the request on every surface.
type OperatorCommandDecision struct {
	Action string
	Note   string
	Stop   <-chan struct{}
	Kill   <-chan struct{}
	Done   func()
}

// OperatorCommandSender is the optional capability of a surface that can show
// an operator command and take the operator's answer. A sender that does not
// implement it is never offered the tool and refuses it if called anyway.
type OperatorCommandSender interface {
	// OperatorCommandsAvailable reports whether a person can answer on this
	// surface during this turn.
	OperatorCommandsAvailable() bool
	// RequestOperatorCommand shows the request and blocks until the operator
	// decides or ctx ends.
	RequestOperatorCommand(ctx context.Context, req OperatorCommandRequest) (*OperatorCommandDecision, error)
}

// SenderUnwrapper is a sender that wraps another one. UnwrapSender returns the
// sender whose capabilities this one forwards, or nil when it forwards none:
// a wrapper decides for itself, and one that says nothing (an embedded
// UpdateSender alone) forwards nothing.
type SenderUnwrapper interface {
	UnwrapSender() UpdateSender
}

// OperatorCommandsOf returns the operator command capability of s when a
// person can answer there now, looking through the wrappers that forward it,
// and nil otherwise.
func OperatorCommandsOf(s UpdateSender) OperatorCommandSender {
	for depth := 0; s != nil && depth < 16; depth++ {
		if oc, ok := s.(OperatorCommandSender); ok {
			if oc.OperatorCommandsAvailable() {
				return oc
			}
			return nil
		}
		u, ok := s.(SenderUnwrapper)
		if !ok {
			return nil
		}
		s = u.UnwrapSender()
	}
	return nil
}

// OperatorCommandTicket is one request's state, shared by a surface and the
// tool: the first fitting answer wins, a run keeps the ticket open until the
// tool reports the end, and every other answer is refused with the reason.
// Surfaces keep tickets in their own hubs; this type is the state machine
// they all run, which is what docs/plans/operator-commands-models checks.
type OperatorCommandTicket struct {
	mu       sync.Mutex
	state    string
	note     string
	decided  chan struct{}
	stop     chan struct{}
	kill     chan struct{}
	finished chan struct{}
}

// NewOperatorCommandTicket returns a ticket waiting for the operator.
func NewOperatorCommandTicket() *OperatorCommandTicket {
	return &OperatorCommandTicket{
		state:    OperatorStateWaiting,
		decided:  make(chan struct{}),
		stop:     make(chan struct{}),
		kill:     make(chan struct{}),
		finished: make(chan struct{}),
	}
}

// State reports the current state.
func (t *OperatorCommandTicket) State() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// Finished is closed once the request is over: declined, ended or abandoned.
func (t *OperatorCommandTicket) Finished() <-chan struct{} { return t.finished }

// Answer applies an operator's answer. It returns ErrOperatorCommandEnded for
// a request that is over and ErrOperatorCommandConflict for an answer the
// state does not admit.
func (t *OperatorCommandTicket) Answer(action, note string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch action {
	case OperatorActionRun, OperatorActionDecline, OperatorActionStop:
	default:
		return fmt.Errorf("%w: %q", ErrOperatorCommandAction, action)
	}
	if t.state == OperatorStateEnded {
		return ErrOperatorCommandEnded
	}
	switch {
	case t.state == OperatorStateWaiting && action == OperatorActionRun:
		t.state = OperatorStateRunning
		close(t.decided)
	case t.state == OperatorStateWaiting && action == OperatorActionDecline:
		t.state = OperatorStateDeclined
		t.note = note
		close(t.decided)
		close(t.finished)
	case t.state == OperatorStateRunning && action == OperatorActionStop:
		t.state = OperatorStateStopping
		close(t.stop)
	case t.state == OperatorStateStopping && action == OperatorActionStop:
		t.state = OperatorStateKilling
		close(t.kill)
	case t.state == OperatorStateKilling && action == OperatorActionStop:
		// Already killing: a third press changes nothing and is not an error.
	default:
		return fmt.Errorf("%w: %s while %s", ErrOperatorCommandConflict, action, t.state)
	}
	return nil
}

// Wait blocks until the operator runs or declines, or ctx ends; a request
// whose wait ended without a decision is over, so a late answer is refused.
func (t *OperatorCommandTicket) Wait(ctx context.Context) (*OperatorCommandDecision, error) {
	select {
	case <-t.decided:
	case <-ctx.Done():
		t.mu.Lock()
		if t.state == OperatorStateWaiting {
			t.state = OperatorStateEnded
			close(t.finished)
			t.mu.Unlock()
			return nil, ctx.Err()
		}
		t.mu.Unlock()
		// The decision landed at the same moment: honour it.
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == OperatorStateDeclined {
		return &OperatorCommandDecision{Action: OperatorActionDecline, Note: t.note, Done: func() {}}, nil
	}
	return &OperatorCommandDecision{Action: OperatorActionRun, Stop: t.stop, Kill: t.kill, Done: t.end}, nil
}

// end closes a request that was run.
func (t *OperatorCommandTicket) end() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == OperatorStateEnded {
		return
	}
	t.state = OperatorStateEnded
	close(t.finished)
}
