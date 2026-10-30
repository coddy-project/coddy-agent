//go:build cli

package cli

import (
	"context"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/remote"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Follow-up prompt suggestions in the console: when a turn of the visible
// session ends well, the backend (the in-process manager or the remote
// server over --remote) predicts what the operator might type
// next over the recorded conversation. The one line it answers with is shown
// dim in the empty editor; Tab or Right takes it into the buffer. What the
// operator then sends is compared with the suggestion (taken counts as
// accepted; typing the same words by hand counts too, the way Claude Code
// counts it) and the outcome feeds the backend's back-off counters.

// suggestionBackend is served by the manager in process, or by the remote
// handler through the server's prediction and outcome routes.
type suggestionBackend interface {
	GeneratePromptSuggestion(ctx context.Context, sessionID string) (string, error)
	NotePromptSuggestionOutcome(sessionID string, accepted bool)
}

// promptSuggestionTimeout bounds the side call; a model slow past it leaves
// the editor empty, which is the state without a suggestion anyway.
const promptSuggestionTimeout = 25 * time.Second

// activeSuggestion is the suggestion of the moment: its text, whether Tab or
// Right already took it, and the generation it belongs to (every turn's
// answer bumps the generation, so a completion that lands late is dropped).
type activeSuggestion struct {
	text     string
	accepted bool
}

// startSuggestion asks the backend for the next-message prediction of
// sessionID and shows it when it lands. Runs on the UI goroutine, at the end
// of a turn of the visible session.
func (a *App) startSuggestion(sessionID string) {
	a.clearSuggestion()
	// A bare App (tests drive the loop without a configuration) asks nothing.
	cfg := a.config()
	if cfg == nil || (a.remoteURL == "" && !cfg.Agent.EffectivePromptSuggestions()) {
		return
	}
	sb, ok := a.mgr.(suggestionBackend)
	if !ok {
		return
	}
	if a.editor == nil || !a.editor.IsEmpty() || a.turnActive || a.modal != nil || a.sessionID != sessionID {
		return
	}
	if a.remoteTurnActive {
		// The response stream can end before the server's idle event arrives.
		// Keep the intent until that event, without declaring the server idle.
		a.suggestionWaiting = true
		return
	}
	a.suggestionGen++
	gen := a.suggestionGen
	ctx, cancel := context.WithCancel(a.workCtx)
	a.suggestionCancel = cancel
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		callCtx, cancelCall := context.WithTimeout(ctx, promptSuggestionTimeout)
		defer cancelCall()
		text, err := sb.GeneratePromptSuggestion(callCtx, sessionID)
		a.postUI(func() { a.applySuggestion(gen, sessionID, text, err) })
	}()
}

// applySuggestion shows the answer of generation gen for sessionID, or drops
// it when the world moved on. Runs on the UI goroutine.
func (a *App) applySuggestion(gen uint64, sessionID, text string, err error) {
	if gen != a.suggestionGen || a.sessionID != sessionID {
		return
	}
	a.suggestionCancel = nil
	if err != nil || strings.TrimSpace(text) == "" {
		if err != nil && !session.IsPromptSuggestionSuppressed(err) && a.log != nil {
			a.log.Debug("prompt suggestion unavailable", "session", sessionID, "error", err)
		}
		// Suppressed, failed, or late: the empty editor stays empty.
		return
	}
	if a.editor == nil || !a.editor.IsEmpty() || a.turnActive || a.remoteTurnActive || a.modal != nil {
		return
	}
	a.suggestion = &activeSuggestion{text: strings.TrimSpace(text)}
	a.editor.SetPlaceholder(a.suggestion.text)
	a.screen.RequestRender()
}

// clearSuggestion takes the suggestion off the editor and cancels a
// prediction still running. Runs on the UI goroutine.
func (a *App) clearSuggestion() {
	a.suggestionGen++
	a.suggestion = nil
	a.suggestionWaiting = false
	if a.suggestionCancel != nil {
		a.suggestionCancel()
		a.suggestionCancel = nil
	}
	if a.editor != nil {
		a.editor.SetPlaceholder("")
	}
}

// settleSuggestion reports what became of the suggestion on screen and takes
// it off, called as the operator submits. Runs on the UI goroutine.
func (a *App) settleSuggestion(text string) {
	s := a.suggestion
	a.clearSuggestion()
	if s == nil {
		return
	}
	accepted := s.accepted || strings.EqualFold(strings.TrimSpace(text), strings.TrimSpace(s.text))
	sessionID := a.sessionID
	if sb, ok := a.mgr.(suggestionBackend); ok {
		a.workers.Add(1)
		go func() {
			defer a.workers.Done()
			sb.NotePromptSuggestionOutcome(sessionID, accepted)
		}()
	}
}

// suggestionAccepted marks the shown suggestion taken (Tab or Right put it in
// the buffer); the outcome is settled when it is submitted.
func (a *App) suggestionAccepted() {
	if a.suggestion != nil {
		a.suggestion.accepted = true
	}
}

var (
	_ suggestionBackend = (*session.Manager)(nil)
	_ suggestionBackend = (*remote.Handler)(nil)
)
