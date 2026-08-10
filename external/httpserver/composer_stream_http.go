//go:build http

package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const composerStreamWaitDeadline = 30 * time.Second

// coddySessionComposerStream streams the same SSE bytes as POST /v1/responses for an in-flight composer turn.
func (s *Server) coddySessionComposerStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}
	hdr := strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID"))
	if hdr != "" && hdr != id {
		http.Error(w, `{"error":{"message":"X-Coddy-Session-ID does not match path id"}}`, http.StatusBadRequest)
		return
	}
	if s.coddyEnsureLoaded(w, r, id) == nil {
		return
	}

	writeSSEHeaders(w)

	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":{"message":"streaming unsupported"}}`, http.StatusInternalServerError)
		return
	}

	// Waiting only pays off while a turn is actually running and its relay has not
	// registered yet. With no turn in flight the client (a panel re-attaching after a
	// reload) must hear that immediately so it can fall back to the persisted transcript.
	if s.peekComposerRelay(id) == nil && !s.sessionTurnActive(id) {
		writeComposerStreamError(w, fl)
		return
	}

	deadline := time.NewTimer(composerStreamWaitDeadline)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		if rel := s.peekComposerRelay(id); rel != nil {
			deadline.Stop()
			ticker.Stop()
			err := rel.serveSubscriber(r.Context(), w)
			if err != nil && !errors.Is(err, context.Canceled) {
				s.log.Warn("composer stream subscriber", "session", id, "error", err)
			}
			return
		}
		if !s.sessionTurnActive(id) {
			writeComposerStreamError(w, fl)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			writeComposerStreamError(w, fl)
			return
		case <-ticker.C:
			_, _ = io.WriteString(w, ": composer stream pending\n\n")
			fl.Flush()
		}
	}
}

// writeSSEHeaders prepares a response for Server-Sent Events.
//
// X-Accel-Buffering: no matters as much as the content type here: nginx and most corporate
// HTTP proxies buffer a text/event-stream body by default, so the whole answer only reaches
// the browser once the turn ends - the turn looks frozen and then appears complete after a
// reload. This header is the standard opt-out.
func writeSSEHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
}

// sessionTurnActive mirrors the turnActive flag of GET /coddy/sessions/{id}/activity: a turn
// running in this process, or a turn holding the on-disk lock from another one.
func (s *Server) sessionTurnActive(id string) bool {
	if s.mgr.SessionTurnActiveInProcess(id) {
		return true
	}
	fs := s.mgr.FileStore()
	if fs == nil || fs.Root == "" {
		return false
	}
	return session.TurnLockHeld(fs.SessionPath(id))
}

// writeComposerStreamError reports "no relay to attach to" in the OpenAI error shape the SPA's
// stream reader understands; a bare {"message":...} reads to it as a dropped stream. The code
// lets a client tell "nothing is running" apart from a real streaming failure.
func writeComposerStreamError(w http.ResponseWriter, fl http.Flusher) {
	_, _ = io.WriteString(w, "event: error\ndata: {\"error\":{\"message\":\"no active composer stream\",\"code\":\"no_active_stream\"}}\n\n")
	fl.Flush()
}
