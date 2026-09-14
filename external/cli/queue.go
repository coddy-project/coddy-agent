//go:build cli

package cli

import (
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// queueWidget renders the follow-ups waiting for the running turn to read them,
// directly above the input, in the order the agent will see them.
//
// It is a live view of the session's queue rather than a transcript log: a
// message read by the agent arrives as an ordinary user block (the
// user_message_chunk of that read) and leaves this list in the same breath, and
// a message taken back with /queue drop leaves without a trace. Nothing here
// scrolls away, so what is still pending is always the last thing above what
// the operator is typing.
type queueWidget struct {
	tui.Container
	theme *tui.Theme
	rows  []acp.QueuedMessage
}

func newQueueWidget(theme *tui.Theme) *queueWidget { return &queueWidget{theme: theme} }

// SetRows replaces what is shown.
//
// A nil receiver is a no-op: an App built without the widget tree (the unit
// tests that drive applyLoopMessage directly) still has to be able to apply an
// update without reaching through a field it never built.
func (q *queueWidget) SetRows(rows []acp.QueuedMessage) {
	if q == nil {
		return
	}
	q.rows = rows
	q.Clear()
	if len(rows) == 0 {
		return
	}
	lines := make([]string, 0, len(rows)+1)
	lines = append(lines, q.theme.Fg(roleDim, fmt.Sprintf("queued for the next step (%d) · /queue to manage", len(rows))))
	for i, r := range rows {
		// The index is what /queue drop names, so it is shown, not the id: an
		// operator types "2", not "q_17".
		head := q.theme.Fg(roleMuted, fmt.Sprintf("%d. ", i+1))
		lines = append(lines, head+q.theme.Fg(roleDim, tui.SanitizeText(queuePreview(r.Text))))
	}
	q.AddChild(tui.NewText(strings.Join(lines, "\n"), 1, 0, nil))
}

// Rows is what the widget is showing, for the /queue command.
func (q *queueWidget) Rows() []acp.QueuedMessage {
	if q == nil {
		return nil
	}
	return q.rows
}

// queuePreview keeps a queued message to one readable line.
const queuePreviewLimit = 96

func queuePreview(text string) string {
	one := strings.Join(strings.Fields(text), " ")
	if len([]rune(one)) <= queuePreviewLimit {
		return one
	}
	return string([]rune(one)[:queuePreviewLimit-1]) + "…"
}

// enqueuePrompt puts what the operator typed during a turn into the session's
// queue, so the running turn reads it at its next step instead of the console
// refusing a second prompt.
//
// A turn that ended between the keystroke and this call answers ErrNoActiveTurn,
// and the text is submitted as an ordinary prompt: what was typed is never lost
// to a race.
func (a *App) enqueuePrompt(text string) {
	body := strings.TrimSpace(text)
	if body == "" {
		return
	}
	sessionID := a.turnSessionID
	if sessionID == "" {
		sessionID = a.sessionID
	}
	_, queue, err := a.mgr.EnqueueTurnMessage(sessionID, body)
	switch {
	case err == nil:
		a.setQueueRows(session.QueuedMessagesWire(queue))
	case isNoActiveTurn(err):
		a.turnActive = false
		a.submitPrompt(body)
	default:
		a.appendStatus(roleWarning, "Could not queue the message: "+err.Error())
	}
}

// isNoActiveTurn reports the one refusal the console answers by sending the
// text as a fresh prompt. The remote backend reports it as text over HTTP, so
// the code is matched as well as the sentinel error.
func isNoActiveTurn(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), "no_active_turn") {
		return true
	}
	return strings.Contains(err.Error(), session.ErrNoActiveTurn.Error())
}

// setQueueRows updates the widget and asks for a repaint.
func (a *App) setQueueRows(rows []acp.QueuedMessage) {
	a.queue.SetRows(rows)
	if a.screen != nil {
		a.screen.RequestRender()
	}
}

// dispatchQueueCommand handles /queue, the console's way of seeing and undoing
// what is waiting. The web composer has a cross on every card; a terminal has a
// command, and the same three actions.
func (a *App) dispatchQueueCommand(args string) {
	sessionID := a.turnSessionID
	if sessionID == "" {
		sessionID = a.sessionID
	}
	arg := strings.TrimSpace(args)
	switch {
	case arg == "" || arg == "list":
		rows, err := a.mgr.QueuedTurnMessages(sessionID)
		if err != nil {
			a.appendStatus(roleWarning, "Could not read the queue: "+err.Error())
			return
		}
		if len(rows) == 0 {
			a.appendStatus(roleDim, "Nothing is queued.")
			return
		}
		for i, r := range rows {
			a.appendStatus(roleDim, fmt.Sprintf("%d. %s", i+1, queuePreview(r.Text)))
		}
	case arg == "clear":
		if err := a.mgr.ClearQueuedTurnMessages(sessionID); err != nil {
			a.appendStatus(roleWarning, "Could not clear the queue: "+err.Error())
			return
		}
		a.setQueueRows(nil)
		a.appendStatus(roleDim, "The queue is empty.")
	case strings.HasPrefix(arg, "drop"):
		rest := strings.TrimSpace(strings.TrimPrefix(arg, "drop"))
		rows, err := a.mgr.QueuedTurnMessages(sessionID)
		if err != nil {
			a.appendStatus(roleWarning, "Could not read the queue: "+err.Error())
			return
		}
		idx := 0
		if _, err := fmt.Sscanf(rest, "%d", &idx); err != nil || idx < 1 || idx > len(rows) {
			a.appendStatus(roleWarning, fmt.Sprintf("Usage: /queue drop <1..%d>", len(rows)))
			return
		}
		left, err := a.mgr.CancelQueuedTurnMessage(sessionID, rows[idx-1].ID)
		if err != nil {
			a.appendStatus(roleWarning, "Could not drop that message: "+err.Error())
			return
		}
		a.setQueueRows(session.QueuedMessagesWire(left))
		a.appendStatus(roleDim, "Dropped from the queue: "+queuePreview(rows[idx-1].Text))
	default:
		a.appendStatus(roleWarning, "Usage: /queue [list|drop <n>|clear]")
	}
}
