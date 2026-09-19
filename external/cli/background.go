//go:build cli

package cli

// Turns a finished background task starts.
//
// The model can start a command or a subagent with notify_on_finish and end its
// turn: the console's waker turns the task's end into a turn of its own. The
// console is where such a turn is least surprising - somebody sits at the
// terminal - so it runs like a typed prompt, on the same UI goroutine, with the
// ordinary sender: a gated tool call opens the permission modal. What differs is
// the first row, which is not there: nobody typed the turn's first message, so
// the transcript goes straight to the agent's answer, and /tasks is where the
// task says it woke the agent.

import (
	"context"
	"errors"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// errConsoleClosing ends a wake whose console is going away. The process is
// exiting; the outcome of the task stays on disk in the session bundle.
var errConsoleClosing = errors.New("the console is closing")

// wakeTurn carries one wake to the UI goroutine, the only place a turn may
// start: it owns the transcript, the spinner and the turn flag.
type wakeTurn struct {
	wake agent.Wake
	done chan error
}

// attachBackgroundWaker lets a task the model started with notify_on_finish
// begin a turn in this console when it ends. A console attached to a remote
// server does not attach one: its tasks run in the server's pool, and the
// server wakes the agent there.
func (a *App) attachBackgroundWaker(pool *bgtask.Pool) {
	agent.NewBackgroundWaker(a.log, a.runWakeTurn).Attach(pool)
}

// runWakeTurn hands one wake to the UI goroutine and blocks until the turn it
// started is over, so the waker keeps its own order: one turn per batch, and
// the next batch only once this one has ended.
func (a *App) runWakeTurn(ctx context.Context, wake agent.Wake) error {
	done := make(chan error, 1)
	select {
	case a.updatesCh <- updateMsg{sessionID: wake.SessionID, update: wakeTurn{wake: wake, done: done}}:
	case <-a.closed:
		return errConsoleClosing
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-done:
		return err
	case <-a.closed:
		return errConsoleClosing
	case <-ctx.Done():
		return ctx.Err()
	}
}

// startWakeTurn is the UI-goroutine half of a wake: it reads the turn state
// that decides whether the turn can start now.
func (a *App) startWakeTurn(w wakeTurn) {
	switch {
	case a.turnActive || a.remoteTurnActive || a.switching || a.shellActive:
		// Busy is not a failure: the waker keeps the batch and asks again,
		// so the outcome arrives once the operator's own turn is over.
		w.done <- session.ErrSessionTurnBusy
	case w.wake.SessionID != a.sessionID:
		// The console shows one conversation. A task of a session the
		// operator has left (/new, /resume) waits for them to come back to
		// it; they are told once where it is waiting.
		a.noteWakeElsewhere(w.wake)
		w.done <- session.ErrSessionTurnBusy
	default:
		// A wake held for this session runs now, so it is no longer held.
		a.forgetHeldWakes(w.wake.SessionID)
		a.startTurnWorker(w.wake.PromptParams(), w.wake.RunOpts(), w.done)
	}
}

// forgetHeldWakes drops what noteWakeElsewhere remembers of a session whose
// wake is running, so the console keeps only the wakes still waiting.
func (a *App) forgetHeldWakes(sessionID string) {
	prefix := sessionID + "\x00"
	for key := range a.wakesHeld {
		if strings.HasPrefix(key, prefix) {
			delete(a.wakesHeld, key)
		}
	}
}

// noteWakeElsewhere says, once per batch, that a task of another session
// finished and where the agent will carry on.
func (a *App) noteWakeElsewhere(wake agent.Wake) {
	ids := make([]string, 0, len(wake.Tasks))
	for _, t := range wake.Tasks {
		ids = append(ids, t.ID)
	}
	key := wake.SessionID + "\x00" + strings.Join(ids, ",")
	if a.wakesHeld == nil {
		a.wakesHeld = map[string]bool{}
	}
	if a.wakesHeld[key] {
		return
	}
	a.wakesHeld[key] = true
	a.appendStatus(roleDim, strings.Join(ids, ", ")+" of session "+wake.SessionID+
		" finished; /resume that session to let the agent carry on there")
}
