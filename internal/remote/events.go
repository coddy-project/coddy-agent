package remote

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// eventsBackoffStart and eventsBackoffMax bound the reconnect delay. The event
// stream is an addition, not a dependency - everything it carries can also be
// read over REST - so a server that never comes back must cost nothing.
const (
	eventsBackoffStart = 1 * time.Second
	eventsBackoffMax   = 15 * time.Second
)

// eventsState is the background subscription to GET /coddy/events.
type eventsState struct {
	eventsMu   sync.Mutex
	eventsStop context.CancelFunc
	eventsWG   sync.WaitGroup
}

// StartEvents subscribes to the server's event stream until Close.
//
// A session on a coddy serve is shared: the browser someone else has open, and
// this console, are two clients of the same turn. What that person queues onto
// the running turn is announced there, and without this subscription a console
// attached over --remote would be the one surface that never heard about it.
//
// Reconnects with capped backoff and is safe to call twice: the previous
// subscription is stopped first. It is started explicitly rather than from
// SetServer, because it holds an HTTP request open for the life of the client:
// only a caller that also calls Close (the console does, through the backend's
// Close hook) may start one.
func (h *Handler) StartEvents() {
	ctx, cancel := context.WithCancel(context.Background())
	h.eventsMu.Lock()
	if h.eventsStop != nil {
		h.eventsStop()
	}
	h.eventsStop = cancel
	h.eventsMu.Unlock()

	h.eventsWG.Add(1)
	go func() {
		defer h.eventsWG.Done()
		h.runEvents(ctx)
	}()
}

// StopEvents ends the subscription and waits for its goroutine.
func (h *Handler) StopEvents() {
	h.eventsMu.Lock()
	stop := h.eventsStop
	h.eventsStop = nil
	h.eventsMu.Unlock()
	if stop != nil {
		stop()
	}
	h.eventsWG.Wait()
}

func (h *Handler) runEvents(ctx context.Context) {
	backoff := eventsBackoffStart
	for ctx.Err() == nil {
		if err := h.readEventsOnce(ctx); err != nil && ctx.Err() == nil {
			h.log.Debug("remote events stream dropped", "error", err, "retry_in", backoff)
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, eventsBackoffMax)
	}
}

// readEventsOnce holds one subscription until it drops.
func (h *Handler) readEventsOnce(ctx context.Context) error {
	req, err := h.newRequest(ctx, http.MethodGet, "/coddy/events", nil)
	if err != nil {
		return err
	}
	res, err := h.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		return h.remoteError(res, body)
	}
	h.beginDetachedPromptSnapshot()
	return readSSE(res.Body, func(f sseFrame) error {
		if ctx.Err() != nil {
			return errStopStream
		}
		h.applyEventFrame(f)
		return nil
	})
}

// applyEventFrame turns one server event into a session update for the console.
// Unknown events are ignored, so a newer server never breaks an older client.
func (h *Handler) applyEventFrame(f sseFrame) {
	switch f.event {
	case "ready":
		h.finishDetachedPromptSnapshot()
		// A reconnect replays starts only. REST also recovers an end or a
		// queue change missed while the connection was down.
		h.refreshKnownSessions()
		return
	case "turn_started", "turn_ended":
		var payload struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal([]byte(f.data), &payload) == nil && payload.SessionID != "" {
			h.applyActivityEvent(payload.SessionID, f.event == "turn_started")
		}
		return
	case "subagent_permission":
		h.applyDetachedPromptEvent(f.data)
		return
	case "background_wake":
		h.applyWakeEvent(f.data)
		return
	case "session_settings":
		h.applySettingsEvent(f.data)
		return
	}
	if f.event != "message_queue" {
		return
	}
	var payload struct {
		SessionID string              `json:"sessionId"`
		Messages  []acp.QueuedMessage `json:"messages"`
		Version   uint64              `json:"version"`
	}
	if json.Unmarshal([]byte(f.data), &payload) != nil || payload.SessionID == "" {
		return
	}
	if payload.Messages == nil {
		payload.Messages = []acp.QueuedMessage{}
	}
	h.publishQueueUpdate(payload.SessionID, acp.MessageQueueUpdate{
		SessionUpdate: acp.UpdateTypeMessageQueue,
		SessionID:     payload.SessionID,
		Messages:      payload.Messages,
		Version:       payload.Version,
	}, queueFence{})
}
