package remote

import (
	"context"
	"fmt"
	"net/url"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// controlUpdateSender is an optional in-process console boundary. It is kept
// separate from acp.UpdateSender so private controls never become wire updates.
type controlUpdateSender interface {
	SendControlUpdate(sessionID string, update any) error
}

// ActivityUpdate is a backend-local control notification for the console, not
// an ACP protocol extension. Revision orders REST reads against live events.
// It describes the server's turn independently of any request owned here.
type ActivityUpdate struct {
	TurnActive bool   `json:"turnActive"`
	Revision   uint64 `json:"-"`
}

// CancelUpdate reports acknowledgement or failure without declaring the server
// idle: admission may still be releasing after a successful cancel request.
type CancelUpdate struct {
	Error string
}

// RefreshSessionState hydrates Stop and queue controls off the caller's
// goroutine. A newer read or lifecycle event invalidates the activity answer;
// queue recovery is fenced against newer reads and deliveries independently
// of activity. No transcript is subscribed.
func (h *Handler) RefreshSessionState(sessionID string) {
	h.mu.Lock()
	st := h.sessions[sessionID]
	if st == nil || h.controlCtx.Err() != nil {
		h.mu.Unlock()
		return
	}
	h.activitySeq++
	revision := h.activitySeq
	st.activityRevision = revision
	st.queue.snapshot++
	fence := queueFence{state: st, epoch: st.queue.epoch, revision: st.queue.revision, snapshot: st.queue.snapshot}
	h.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(h.controlCtx, restTimeout)
		defer cancel()
		active, err := h.sessionActivity(ctx, sessionID)
		if err != nil {
			if ctx.Err() == nil {
				h.log.Warn("remote session activity", "session", sessionID, "error", err)
			}
		} else {
			h.mu.Lock()
			current := h.sessions[sessionID] == st && st.activityRevision == revision && ctx.Err() == nil
			h.mu.Unlock()
			if current {
				h.sendActivity(sessionID, active, revision)
			}
		}

		var queue queueResponse
		if err := h.getJSON(ctx, queuePath(sessionID), &queue); err != nil {
			if !isNotFound(err) && ctx.Err() == nil {
				h.log.Warn("remote session queue", "session", sessionID, "error", err)
			}
			return
		}
		h.mu.Lock()
		current := h.sessions[sessionID] == st && ctx.Err() == nil
		h.mu.Unlock()
		if current {
			h.publishQueue(sessionID, queue, fence)
		}
	}()
}

func (h *Handler) sessionActivity(ctx context.Context, sessionID string) (bool, error) {
	var out struct {
		SessionID  string `json:"sessionId"`
		TurnActive *bool  `json:"turnActive"`
	}
	err := h.getJSON(ctx, "/coddy/sessions/"+url.PathEscape(sessionID)+"/activity", &out)
	if isNotFound(err) {
		return false, nil // a newly minted remote session has no bundle yet
	}
	if err != nil {
		return false, err
	}
	if out.TurnActive == nil || (out.SessionID != "" && out.SessionID != sessionID) {
		return false, fmt.Errorf("remote coddy: invalid session activity answer")
	}
	return *out.TurnActive, nil
}

func (h *Handler) applyActivityEvent(sessionID string, active bool) {
	h.mu.Lock()
	st := h.sessions[sessionID]
	if st == nil || h.controlCtx.Err() != nil {
		h.mu.Unlock()
		return
	}
	h.activitySeq++
	revision := h.activitySeq
	st.activityRevision = revision
	h.mu.Unlock()
	h.sendActivity(sessionID, active, revision)
}

func (h *Handler) sendActivity(sessionID string, active bool, revision uint64) {
	if sender, ok := h.currentSender().(controlUpdateSender); ok && h.controlCtx.Err() == nil {
		_ = sender.SendControlUpdate(sessionID, ActivityUpdate{TurnActive: active, Revision: revision})
	}
}

func sendCancelUpdate(sender acp.UpdateSender, sessionID string, update CancelUpdate) {
	if controls, ok := sender.(controlUpdateSender); ok {
		_ = controls.SendControlUpdate(sessionID, update)
		return
	}
	// session/cancel has no response in ACP; a standard text chunk keeps a
	// failure visible without adding a private shape to the protocol.
	if sender != nil && update.Error != "" {
		_ = sender.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
			SessionUpdate: acp.UpdateTypeAgentMessageChunk,
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "Could not stop the turn: " + update.Error + "\n"},
		})
	}
}

func (h *Handler) refreshKnownSessions() {
	h.mu.Lock()
	ids := make([]string, 0, len(h.sessions))
	for id := range h.sessions {
		ids = append(ids, id)
	}
	h.mu.Unlock()
	for _, id := range ids {
		h.RefreshSessionState(id)
	}
}
