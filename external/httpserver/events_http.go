//go:build http

package httpserver

import (
	"io"
	"net/http"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// serverEventsKeepalive bounds how long an idle events stream stays silent. Proxies and
// load balancers drop a connection that says nothing for a few minutes.
const serverEventsKeepalive = 20 * time.Second

// coddyEventsStream streams session-independent events, currently the turn lifecycle.
//
// It exists so a client can be told a turn started in a session it is not driving. Without
// it the only way to notice is polling GET /coddy/sessions, which means the start of every
// turn in a long-running loop is missed and watched live only from its middle.
func (s *Server) coddyEventsStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":{"message":"streaming unsupported"}}`, http.StatusInternalServerError)
		return
	}

	frames, unsubscribe := s.events.subscribe()
	defer unsubscribe()

	writeSSEHeaders(w)
	// Subscribe before the snapshot, so a turn starting in between is delivered twice
	// rather than lost; a repeated turn_started is idempotent for every consumer.
	_, _ = io.WriteString(w, "retry: 3000\n\n")
	for _, id := range s.mgr.ActiveTurnSessionIDs() {
		_, _ = w.Write(turnEventFrame(session.TurnEvent{
			SessionID: id,
			Phase:     session.TurnPhaseStarted,
			At:        time.Now().UTC(),
		}))
	}
	// The snapshot is what makes a client connecting mid-turn see the turn at all, and
	// "ready" is how it knows the snapshot is complete rather than still arriving.
	_, _ = io.WriteString(w, "event: ready\ndata: {\"object\":\"coddy.events_ready\"}\n\n")
	fl.Flush()

	keepalive := time.NewTicker(serverEventsKeepalive)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case frame, open := <-frames:
			if !open {
				return
			}
			if _, err := w.Write(frame); err != nil {
				return
			}
			fl.Flush()
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
