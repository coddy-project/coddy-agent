//go:build http

package httpserver

import (
	"encoding/json"
	"sync"
	"time"

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
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	name := "turn_started"
	if ev.Phase == session.TurnPhaseEnded {
		name = "turn_ended"
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
