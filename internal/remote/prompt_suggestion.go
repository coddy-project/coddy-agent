package remote

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// GeneratePromptSuggestion reads the server's prediction. The server owns
// its model, history, feature switch and context strategy; the console only
// displays the answer. An older server without this route offers no hint.
func (h *Handler) GeneratePromptSuggestion(ctx context.Context, sessionID string) (string, error) {
	id := strings.TrimSpace(sessionID)
	if err := session.ValidateFolderSessionID(id); err != nil {
		return "", err
	}
	if err := h.controlCtx.Err(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(h.controlCtx, cancel)
	defer func() { stop(); cancel() }()
	var out struct {
		SessionID string `json:"sessionId"`
		Text      string `json:"text"`
	}
	err := h.getJSON(ctx, suggestionPath(id), &out)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if isNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if out.SessionID != id {
		return "", fmt.Errorf("remote: suggestion belongs to another session")
	}
	return strings.TrimSpace(out.Text), nil
}

// NotePromptSuggestionOutcome reports acceptance or ignoring to the same
// server, so its back-off counters include remote consoles. The UI calls it
// on a worker; the short bound and control context also cover console exit.
func (h *Handler) NotePromptSuggestionOutcome(sessionID string, accepted bool) {
	id := strings.TrimSpace(sessionID)
	if session.ValidateFolderSessionID(id) != nil || h.controlCtx.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(h.controlCtx, 5*time.Second)
	defer cancel()
	err := h.postJSON(ctx, suggestionPath(id)+"/outcome", map[string]bool{"accepted": accepted}, nil)
	if err != nil && !isNotFound(err) && ctx.Err() == nil {
		h.log.Debug("remote prompt suggestion outcome", "session", id, "error", err)
	}
}

func suggestionPath(id string) string {
	return "/coddy/sessions/" + url.PathEscape(id) + "/prompt-suggestion"
}

// ConfigReloadedUpdate is a private console notification, not an ACP frame.
// The console retires hints made with the server's outgoing configuration.
type ConfigReloadedUpdate struct{}

func (h *Handler) sendConfigReloaded() {
	if sender, ok := h.currentSender().(controlUpdateSender); ok && h.controlCtx.Err() == nil {
		_ = sender.SendControlUpdate("", ConfigReloadedUpdate{})
	}
}
