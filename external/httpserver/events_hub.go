//go:build http

package httpserver

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// serverEventsHub fans server-wide events out to every subscriber of GET /coddy/events.
//
// Same shape as composerStreamRelay, minus the replay buffer: a client that connects late
// is caught up from the active-turn registry rather than from a history of frames, so
// there is nothing to keep. Sends are non-blocking for the same reason as there - a slow
// subscriber must never be able to stall a turn that is publishing.
type serverEventsHub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newServerEventsHub() *serverEventsHub {
	return &serverEventsHub{subs: make(map[chan []byte]struct{})}
}

func (h *serverEventsHub) subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
		})
	}
}

func (h *serverEventsHub) publish(frame []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- frame:
		default:
		}
	}
}

// turnEventFrame renders a session turn edge as one SSE frame.
//
// The payload stays minimal on purpose: it is built on the turn's own goroutine, so it
// must not read the session bundle. A client that wants titles or activity counters asks
// GET /coddy/sessions once the event tells it something changed.
func turnEventFrame(ev session.TurnEvent) []byte {
	payload := map[string]interface{}{
		"object":    "coddy.turn_event",
		"sessionId": ev.SessionID,
		"phase":     string(ev.Phase),
		"at":        ev.At.UTC().Format(time.RFC3339Nano),
	}
	name := "turn_started"
	switch ev.Phase {
	case session.TurnPhaseEnded:
		name = "turn_ended"
	case session.TurnPhaseWoken:
		// The turn holding the session was started by finished background
		// tasks. The tasks travel with it: a client that follows only its own
		// turns decides from this frame whether the turn is worth following.
		name = "background_wake"
		payload["object"] = "coddy.background_wake"
		payload["tasks"] = session.BackgroundWakeUpdate(ev.Wake).Tasks
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	frame := make([]byte, 0, len(body)+len(name)+16)
	frame = append(frame, "event: "...)
	frame = append(frame, name...)
	frame = append(frame, "\ndata: "...)
	frame = append(frame, body...)
	frame = append(frame, "\n\n"...)
	return frame
}

// publishTurnEvent is the Manager observer this server registers in New.
func (s *Server) publishTurnEvent(ev session.TurnEvent) {
	if s.events == nil {
		return
	}
	if frame := turnEventFrame(ev); frame != nil {
		s.events.publish(frame)
	}
}

// messageQueueFrame renders a session's message queue as one SSE frame.
//
// Unlike the turn edges, this one carries its payload: the queue is small, it
// is what every client renders, and a client told only that "something changed"
// would have to fetch the session it may not even have open.
func messageQueueFrame(u acp.MessageQueueUpdate) []byte {
	if u.Messages == nil {
		u.Messages = []acp.QueuedMessage{}
	}
	body, err := json.Marshal(map[string]interface{}{
		"object":    "coddy.message_queue",
		"sessionId": u.SessionID,
		"messages":  u.Messages,
		"version":   u.Version,
	})
	if err != nil {
		return nil
	}
	frame := make([]byte, 0, len(body)+32)
	frame = append(frame, "event: message_queue\ndata: "...)
	frame = append(frame, body...)
	frame = append(frame, "\n\n"...)
	return frame
}

// publishMessageQueueEvent is the Manager observer this server registers in New.
// It is called on the goroutine that changed the queue, so it only renders a
// frame and hands it to the hub, whose sends are non-blocking.
func (s *Server) publishMessageQueueEvent(u acp.MessageQueueUpdate) {
	if s.events == nil {
		return
	}
	if frame := messageQueueFrame(u); frame != nil {
		s.events.publish(frame)
	}
}

// sessionSettingsFrame renders a change of a session's settings as one SSE
// frame: the whole snapshot with its version, what changed and who asked.
func sessionSettingsFrame(u acp.SessionSettingsUpdate) []byte {
	body, err := json.Marshal(map[string]interface{}{
		"object":    "coddy.session_settings",
		"sessionId": u.Settings.SessionID,
		"settings":  u.Settings,
		"notice":    u.Notice,
		"source":    u.Source,
	})
	if err != nil {
		return nil
	}
	frame := make([]byte, 0, len(body)+32)
	frame = append(frame, "event: session_settings\ndata: "...)
	frame = append(frame, body...)
	frame = append(frame, "\n\n"...)
	return frame
}

// publishSessionSettingsEvent is the Manager observer this server registers
// in New; like the queue observer it only renders and hands off.
func (s *Server) publishSessionSettingsEvent(u acp.SessionSettingsUpdate) {
	if s.events == nil {
		return
	}
	if frame := sessionSettingsFrame(u); frame != nil {
		s.events.publish(frame)
	}
}

// configReloadedFrame renders a swap of the live configuration as one SSE frame.
//
// As thin as turnEventFrame, and for the same reason: what a reload changed is already
// on the wire behind GET /v1/models and GET /coddy/slash-commands, so a payload that
// tried to carry it would be a second copy to keep in step with both. The event says
// only that those answers moved; the client re-reads whichever one it renders.
func configReloadedFrame(at time.Time) []byte {
	body, err := json.Marshal(map[string]interface{}{
		"object": "coddy.config_reloaded",
		"at":     at.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil
	}
	frame := make([]byte, 0, len(body)+40)
	frame = append(frame, "event: config_reloaded\ndata: "...)
	frame = append(frame, body...)
	frame = append(frame, "\n\n"...)
	return frame
}

// publishConfigReloaded announces a reload to every events subscriber. ReplaceConfig is
// its only caller, so every path that installs a new configuration - the settings form,
// the agent's config_commit and config_rollback, a skill install - announces it without
// each having to remember to.
func (s *Server) publishConfigReloaded() {
	if s.events == nil {
		return
	}
	if frame := configReloadedFrame(time.Now()); frame != nil {
		s.events.publish(frame)
	}
}

// sessionRewoundFrame renders an in-place history truncation as one SSE frame.
func sessionRewoundFrame(sessionID string, messagesRev uint64) []byte {
	body, err := json.Marshal(map[string]interface{}{
		"object":      "coddy.session_rewound",
		"sessionId":   sessionID,
		"messagesRev": messagesRev,
	})
	if err != nil {
		return nil
	}
	frame := make([]byte, 0, len(body)+40)
	frame = append(frame, "event: session_rewound\ndata: "...)
	frame = append(frame, body...)
	frame = append(frame, "\n\n"...)
	return frame
}

// publishSessionRewound tells every events subscriber that a session's history
// was truncated in place, so a watcher of that session refetches its
// transcript instead of keeping a tail that no longer exists. coddyRewind is
// its only caller.
func (s *Server) publishSessionRewound(sessionID string, messagesRev uint64) {
	if s.events == nil {
		return
	}
	if frame := sessionRewoundFrame(sessionID, messagesRev); frame != nil {
		s.events.publish(frame)
	}
}
