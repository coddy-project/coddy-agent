//go:build cli

package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
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
	// version is the highest queue version rendered. The same change reaches
	// this console down the turn's stream and down the server's event stream,
	// which are separate connections: without this an older frame could put a
	// message the operator took back on screen again.
	version uint64
	// Remote recovery is ordered by the handler, not by server versions alone.
	remoteEpoch    uint64
	remoteRevision uint64
}

func newQueueWidget(theme *tui.Theme) *queueWidget { return &queueWidget{theme: theme} }

// SetRows replaces what is shown.
//
// A nil receiver is a no-op: an App built without the widget tree (the unit
// tests that drive applyLoopMessage directly) still has to be able to apply an
// update without reaching through a field it never built.
// Apply renders a queue stamped with version, ignoring an update older than
// what is already on screen. Zero is also a snapshot version, not a reset.
func (q *queueWidget) Apply(rows []acp.QueuedMessage, version uint64) {
	if q == nil || version < q.version {
		return
	}
	q.version = version
	q.SetRows(rows)
}

// ApplyControl adopts a handler-fenced view atomically. A newer notification
// can overtake the recovery snapshot, so rows and version must travel together.
func (q *queueWidget) ApplyControl(update remote.QueueControlUpdate) {
	if q == nil || update.Epoch < q.remoteEpoch || update.Revision < q.remoteRevision {
		return
	}
	q.remoteEpoch, q.remoteRevision = update.Epoch, update.Revision
	q.version = update.Version
	q.SetRows(update.Messages)
}

// Reset starts the version history of a newly selected session.
func (q *queueWidget) Reset() {
	if q != nil {
		q.version = 0
		q.remoteEpoch, q.remoteRevision = 0, 0
		q.SetRows(nil)
	}
}

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
	lines = append(lines, q.theme.Fg(roleDim, fmt.Sprintf("queued messages (%d) · /queue to manage", len(rows))))
	for i, r := range rows {
		// The index is what /queue drop names, so it is shown, not the id: an
		// operator types "2", not "q_17".
		head := q.theme.Fg(roleMuted, fmt.Sprintf("%d. ", i+1))
		mode := r.Mode
		if mode == "" {
			mode = string(session.QueueModeSteer)
		}
		lines = append(lines, head+q.theme.Fg(roleDim, "["+mode+"] "+tui.SanitizeText(queuePreview(r.Text))))
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
// Refusal restores the draft. In particular, queue admission can close before
// the owned prompt returns: that is not permission to start another worker.
func (a *App) enqueuePrompt(text string) {
	a.enqueuePromptWithMode(text, session.QueueModeSteer, false)
}

func oppositeQueueMode(mode session.QueueMode) session.QueueMode {
	if mode == session.QueueModeAfterTurn {
		return session.QueueModeSteer
	}
	return session.QueueModeAfterTurn
}

func (a *App) submitQueueChoice(text string, alternate bool) {
	mode := a.queuePreference
	if !session.ValidQueueMode(mode) {
		if a.pendingQueueText != "" {
			// A second message while the choice is still open joins the first
			// rather than replacing it: both wait for the same answer, and the
			// first keeps the key it was sent with.
			text, alternate = a.pendingQueueText+"\n"+text, a.pendingQueueAlternate
		}
		a.pendingQueueText, a.pendingQueueAlternate = text, alternate
		a.appendStatus(roleDim, "Choose the default queue mode once: 1 Steer next step · 2 After this turn (Esc restores draft)")
		return
	}
	if alternate {
		mode = oppositeQueueMode(mode)
	}
	a.enqueuePromptWithMode(text, mode, false)
}

func (a *App) enqueuePromptWithMode(text string, mode session.QueueMode, savePreference bool) {
	body := strings.TrimSpace(text)
	if body == "" {
		return
	}
	sessionID, mgr, local := a.sessionID, a.mgr, a.remoteURL == ""
	preferred := a.queuePreference
	a.runQueueRequest(sessionID, func() queueResult {
		if savePreference {
			if writer, ok := mgr.(interface{ SetQueueModePreference(session.QueueMode) error }); ok {
				if err := writer.SetQueueModePreference(preferred); err != nil {
					return queueResult{action: "enqueue", text: body, err: fmt.Errorf("save queue preference: %w", err)}
				}
			}
		}
		// Settings commands at the start apply at once and never reach the
		// model; only the rest waits for the turn (session.EnqueueFollowUp).
		var queued bool
		var err error
		if withMode, ok := mgr.(interface {
			EnqueueFollowUpWithMode(context.Context, string, string, string, session.QueueMode, []acp.ImagePartRef) (session.QueuedMessage, bool, string, error)
		}); ok {
			_, queued, _, err = withMode.EnqueueFollowUpWithMode(context.Background(), sessionID, body, "console", mode, nil)
		} else {
			_, queued, _, err = mgr.EnqueueFollowUp(context.Background(), sessionID, body, "console")
		}
		if err != nil {
			return queueResult{action: "enqueue", text: body, err: fmt.Errorf("could not queue the message: %w", err)}
		}
		if !queued {
			return queueResult{action: "settings", text: body}
		}
		// Remote rows arrive as versioned backend updates; the in-process
		// queue is read back here, as the enqueue itself used to answer.
		var rows []session.QueuedMessage
		if local {
			rows, _ = mgr.QueuedTurnMessages(sessionID)
		}
		return queueResult{action: "enqueue", text: body, rows: rows}
	})
}

// isNoActiveTurn identifies the refusal that needs activity reconciliation.
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
	sessionID, mgr := a.sessionID, a.mgr
	arg := strings.TrimSpace(args)
	a.runQueueRequest(sessionID, func() queueResult { return runQueueCommand(mgr, sessionID, arg) })
}

type queueResult struct {
	action string
	text   string
	rows   []session.QueuedMessage
	err    error
}

// Only remote queue operations involve network I/O. Their rows arrive as
// versioned backend updates; the worker result carries receipts and errors.
func (a *App) runQueueRequest(sessionID string, work func() queueResult) {
	if a.remoteURL == "" {
		a.applyQueueResult(work())
		return
	}
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		_ = a.Sender().SendSessionUpdate(sessionID, work())
	}()
}

func (a *App) refreshRemoteControls() {
	if refresher, ok := a.mgr.(interface{ RefreshSessionState(string) }); ok {
		refresher.RefreshSessionState(a.sessionID)
	}
}

func (a *App) applyQueueResult(u queueResult) {
	if u.err != nil {
		if u.action == "enqueue" {
			a.restoreDraft(u.text)
			if isNoActiveTurn(u.err) {
				a.refreshRemoteControls()
			}
		}
		a.appendStatus(roleWarning, u.err.Error())
		return
	}
	// A text that was only settings commands queued nothing: the notice
	// arrives as a session_settings update, and the queue is what it was.
	if u.action == "settings" {
		return
	}
	if a.remoteURL == "" && u.action != "list" {
		a.setQueueRows(session.QueuedMessagesWire(u.rows))
	}
	switch u.action {
	case "list":
		if len(u.rows) == 0 {
			a.appendStatus(roleDim, "Nothing is queued.")
		}
		for i, row := range u.rows {
			a.appendStatus(roleDim, fmt.Sprintf("%d. [%s] %s", i+1, row.Mode, queuePreview(row.Text)))
		}
	case "mode":
		a.appendStatus(roleDim, "Queued message mode changed.")
	case "clear":
		a.appendStatus(roleDim, "The queue is empty.")
	case "drop":
		// Taken back before the turn read it: the text returns to the input,
		// the way the browser's cross gives it back - taking a message back is
		// how it gets edited.
		a.restoreDraft(u.text)
		a.appendStatus(roleDim, "Taken back into the input: "+queuePreview(u.text))
	}
}

// restoreDraft puts text back into the input, ahead of anything typed since.
func (a *App) restoreDraft(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if draft := a.editor.PendingText(); draft != "" {
		text += "\n" + draft
	}
	a.editor.SetText(text)
}

// queueVerb is the first word of a /queue argument: "/queue model" is not a
// misspelt "/queue mode".
func queueVerb(arg string) string {
	verb, _, _ := strings.Cut(strings.TrimSpace(arg), " ")
	return verb
}

func runQueueCommand(mgr backend, sessionID, arg string) queueResult {
	switch {
	case arg == "" || arg == "list":
		rows, err := mgr.QueuedTurnMessages(sessionID)
		if err != nil {
			err = fmt.Errorf("could not read the queue: %w", err)
		}
		return queueResult{action: "list", rows: rows, err: err}
	case arg == "clear":
		if err := mgr.ClearQueuedTurnMessages(sessionID); err != nil {
			return queueResult{err: fmt.Errorf("could not clear the queue: %w", err)}
		}
		return queueResult{action: "clear"}
	case queueVerb(arg) == "drop":
		rest := strings.TrimSpace(strings.TrimPrefix(arg, "drop"))
		rows, err := mgr.QueuedTurnMessages(sessionID)
		if err != nil {
			return queueResult{err: fmt.Errorf("could not read the queue: %w", err)}
		}
		idx := 0
		if _, err := fmt.Sscanf(rest, "%d", &idx); err != nil || idx < 1 || idx > len(rows) {
			return queueResult{err: fmt.Errorf("Usage: /queue drop <1..%d>", len(rows))}
		}
		left, err := mgr.CancelQueuedTurnMessage(sessionID, rows[idx-1].ID)
		if err != nil {
			return queueResult{err: fmt.Errorf("could not drop that message: %w", err)}
		}
		return queueResult{action: "drop", rows: left, text: rows[idx-1].Text}
	case queueVerb(arg) == "mode":
		var idx int
		var rawMode string
		if _, err := fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(arg, "mode")), "%d %s", &idx, &rawMode); err != nil || !session.ValidQueueMode(session.QueueMode(rawMode)) {
			return queueResult{err: fmt.Errorf("Usage: /queue mode <n> <steer|after_turn>")}
		}
		rows, err := mgr.QueuedTurnMessages(sessionID)
		if err != nil {
			return queueResult{err: err}
		}
		if idx < 1 || idx > len(rows) {
			return queueResult{err: fmt.Errorf("queue index out of range")}
		}
		setter, ok := mgr.(interface {
			SetQueuedTurnMessageMode(string, string, session.QueueMode) ([]session.QueuedMessage, error)
		})
		if !ok {
			return queueResult{err: fmt.Errorf("queue mode switch is unavailable")}
		}
		left, err := setter.SetQueuedTurnMessageMode(sessionID, rows[idx-1].ID, session.QueueMode(rawMode))
		return queueResult{action: "mode", rows: left, err: err}
	default:
		return queueResult{err: fmt.Errorf("Usage: /queue [list|drop <n>|mode <n> <steer|after_turn>|clear]")}
	}
}
