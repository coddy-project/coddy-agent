package remote

import (
	"fmt"
	"net/url"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// queueResponse is what the queue routes answer with.
type queueResponse struct {
	Message  *session.QueuedMessage  `json:"message"`
	Messages []session.QueuedMessage `json:"messages"`
	Version  uint64                  `json:"version"`
}

// QueueControlUpdate is an accepted queue view for the in-process console.
// Epoch fences requests from before a version reset; Revision orders deliveries
// prepared concurrently. Neither extends the public ACP queue update.
type QueueControlUpdate struct {
	acp.MessageQueueUpdate
	Epoch    uint64
	Revision uint64
}

// queueOrder is protected by Handler.mu. Only an uncontested latest snapshot
// may lower the high-water mark, including when the restarted server is active.
type queueOrder struct {
	version  uint64
	epoch    uint64
	revision uint64
	snapshot uint64
}

type queueFence struct {
	state    *sessionState
	epoch    uint64
	revision uint64
	snapshot uint64
}

func (h *Handler) queueRequestFence(sessionID string) queueFence {
	st := h.session(sessionID)
	h.mu.Lock()
	defer h.mu.Unlock()
	return queueFence{state: st, epoch: st.queue.epoch}
}

func (q *queueOrder) accept(version uint64, fence queueFence) bool {
	if fence.state != nil && fence.epoch != q.epoch {
		return false
	}
	if fence.snapshot != 0 {
		if fence.snapshot != q.snapshot || fence.revision != q.revision {
			return false
		}
		if version < q.version {
			q.epoch++
		}
	} else if version < q.version {
		if fence.state == nil {
			// Even a low live frame may be from a restarted server. It cannot
			// reset the queue, but a snapshot it crossed is no longer fresh.
			q.revision++
		}
		return false
	}
	q.version = version
	q.revision++
	return true
}

// publishQueue preserves the snapshot's version all the way to the console.
// The legacy manager-shaped return values are receipts, not unversioned UI
// replacements: the same queue may already have advanced on the event stream.
func (h *Handler) publishQueue(sessionID string, out queueResponse, fence queueFence) bool {
	return h.publishQueueUpdate(sessionID, acp.MessageQueueUpdate{
		SessionUpdate: acp.UpdateTypeMessageQueue,
		SessionID:     sessionID,
		Messages:      session.QueuedMessagesWire(out.Messages),
		Version:       out.Version,
	}, fence)
}

func (h *Handler) publishQueueUpdate(sessionID string, update acp.MessageQueueUpdate, fence queueFence) bool {
	h.mu.Lock()
	st, sender := h.sessions[sessionID], h.sender
	if h.controlCtx.Err() != nil || (fence.state != nil && st != fence.state) {
		h.mu.Unlock()
		return false
	}
	control := QueueControlUpdate{MessageQueueUpdate: update}
	if st != nil {
		if !st.queue.accept(update.Version, fence) {
			h.mu.Unlock()
			return false
		}
		control.Epoch, control.Revision = st.queue.epoch, st.queue.revision
	}
	h.mu.Unlock()
	if sender == nil || h.controlCtx.Err() != nil {
		return true
	}
	if controls, ok := sender.(controlUpdateSender); ok && st != nil {
		_ = controls.SendControlUpdate(sessionID, control)
	} else {
		_ = sender.SendSessionUpdate(sessionID, update)
	}
	return true
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
	fence := h.queueRequestFence(sessionID)
	var out queueResponse
	err := h.postJSON(h.controlCtx, queuePath(sessionID), map[string]string{"text": text}, &out)
	if err != nil {
		return session.QueuedMessage{}, nil, translateQueueError(err)
	}
	if out.Message == nil {
		return session.QueuedMessage{}, out.Messages, fmt.Errorf("remote coddy: the queue answered without a message")
	}
	h.publishQueue(sessionID, out, fence)
	return *out.Message, out.Messages, nil
}

// QueuedTurnMessages lists what the remote session is holding.
func (h *Handler) QueuedTurnMessages(sessionID string) ([]session.QueuedMessage, error) {
	fence := h.queueRequestFence(sessionID)
	var out queueResponse
	if err := h.getJSON(h.controlCtx, queuePath(sessionID), &out); err != nil {
		return nil, translateQueueError(err)
	}
	h.publishQueue(sessionID, out, fence)
	return out.Messages, nil
}

// CancelQueuedTurnMessage takes one queued message back on the remote server.
func (h *Handler) CancelQueuedTurnMessage(sessionID, messageID string) ([]session.QueuedMessage, error) {
	fence := h.queueRequestFence(sessionID)
	var out queueResponse
	err := h.deleteJSON(h.controlCtx, queuePath(sessionID)+"/"+url.PathEscape(messageID), &out)
	if err != nil {
		return nil, translateQueueError(err)
	}
	h.publishQueue(sessionID, out, fence)
	return out.Messages, nil
}

// ClearQueuedTurnMessages empties the remote session's queue.
func (h *Handler) ClearQueuedTurnMessages(sessionID string) error {
	fence := h.queueRequestFence(sessionID)
	var out queueResponse
	if err := h.deleteJSON(h.controlCtx, queuePath(sessionID), &out); err != nil {
		return translateQueueError(err)
	}
	h.publishQueue(sessionID, out, fence)
	return nil
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
