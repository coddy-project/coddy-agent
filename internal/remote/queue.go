package remote

import (
	"context"
	"fmt"
	"net/url"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// queueResponse is what the queue routes answer with.
type queueResponse struct {
	Message  *session.QueuedMessage  `json:"message"`
	Messages []session.QueuedMessage `json:"messages"`
}

// queuePath is the queue of one session on the remote server.
func queuePath(sessionID string) string {
	return "/coddy/sessions/" + url.PathEscape(sessionID) + "/queue"
}

// EnqueueTurnMessage queues a follow-up for the turn running on the remote
// server, so the console behaves the same whether it drives a local manager or
// a remote one.
//
// A refusal the local manager reports as session.ErrNoActiveTurn comes back
// here as a 409 carrying that code; it is translated back, so the caller's
// errors.Is keeps working across the wire.
func (h *Handler) EnqueueTurnMessage(sessionID, text string) (session.QueuedMessage, []session.QueuedMessage, error) {
	var out queueResponse
	err := h.postJSON(context.Background(), queuePath(sessionID), map[string]string{"text": text}, &out)
	if err != nil {
		return session.QueuedMessage{}, nil, translateQueueError(err)
	}
	if out.Message == nil {
		return session.QueuedMessage{}, out.Messages, fmt.Errorf("remote coddy: the queue answered without a message")
	}
	return *out.Message, out.Messages, nil
}

// QueuedTurnMessages lists what the remote session is holding.
func (h *Handler) QueuedTurnMessages(sessionID string) ([]session.QueuedMessage, error) {
	var out queueResponse
	if err := h.getJSON(context.Background(), queuePath(sessionID), &out); err != nil {
		return nil, translateQueueError(err)
	}
	return out.Messages, nil
}

// CancelQueuedTurnMessage takes one queued message back on the remote server.
func (h *Handler) CancelQueuedTurnMessage(sessionID, messageID string) ([]session.QueuedMessage, error) {
	var out queueResponse
	err := h.deleteJSON(context.Background(), queuePath(sessionID)+"/"+url.PathEscape(messageID), &out)
	if err != nil {
		return nil, translateQueueError(err)
	}
	return out.Messages, nil
}

// ClearQueuedTurnMessages empties the remote session's queue.
func (h *Handler) ClearQueuedTurnMessages(sessionID string) error {
	return translateQueueError(h.deleteJSON(context.Background(), queuePath(sessionID), nil))
}

// translateQueueError restores the sentinel a local manager would have
// returned, so one branch in the console serves both backends.
func translateQueueError(err error) error {
	if err == nil {
		return nil
	}
	switch queueErrorCode(err) {
	case "no_active_turn":
		return session.ErrNoActiveTurn
	case "queue_full":
		return session.ErrQueueFull
	case "not_found":
		return session.ErrQueuedMessageNotFound
	case "subagent_read_only":
		return fmt.Errorf("%w: the remote session is a subagent run", session.ErrSubagentReadOnly)
	}
	return err
}
