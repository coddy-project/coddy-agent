//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// registerRewindRoute adds the in-place history rewind endpoint.
func (s *Server) registerRewindRoute() {
	s.mux.HandleFunc("POST /coddy/sessions/{id}/rewind", s.coddyRewind)
}

// coddyRewind handles POST /coddy/sessions/{id}/rewind.
//
// Request body:
//
//	{ "userMessageIndex": <int> }
//
// Response:
//
//	{ "object": "coddy.session_rewound", "sessionId": "...", "messagesRev": N }
func (s *Server) coddyRewind(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}

	var body struct {
		UserMessageIndex *int `json:"userMessageIndex"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	// The index is required: a missing one would decode as 0 and wipe
	// everything after the first user message.
	if body.UserMessageIndex == nil {
		http.Error(w, `{"error":{"message":"userMessageIndex is required"}}`, http.StatusBadRequest)
		return
	}
	if *body.UserMessageIndex < 0 {
		http.Error(w, `{"error":{"message":"userMessageIndex must be >= 0"}}`, http.StatusBadRequest)
		return
	}

	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	// A child transcript is read-only on every surface, and so is the session
	// of a scheduler job: rewinding either would rewrite history nobody may
	// change.
	if st.IsReadOnlyTranscript() {
		writeSubagentsError(w, http.StatusConflict, subagentReadOnlyMessage(st))
		return
	}
	// A turn in flight would append onto a history that no longer matches what
	// it was started on; the message queue is empty exactly while no turn runs,
	// so refusing here covers the queued case too. RewindSession then holds the
	// prompt turn lock across the cut, so a turn admitted between this probe
	// and the truncation either already shows in the flag or waits for the lock
	// and runs on the rewound history.
	if s.sessionTurnActive(id) {
		writeSubagentsError(w, http.StatusConflict, "session "+id+" has a turn in flight")
		return
	}

	rev, err := s.mgr.RewindSession(id, *body.UserMessageIndex)
	if err != nil {
		switch {
		case errors.Is(err, session.ErrRewindOutOfRange):
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		case errors.Is(err, session.ErrSessionTurnActive),
			errors.Is(err, session.ErrSubagentReadOnly),
			errors.Is(err, session.ErrSchedulerSessionReadOnly):
			writeSubagentsError(w, http.StatusConflict, err.Error())
		default:
			s.log.Error("rewind session", "session", id, "error", err)
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusInternalServerError)
		}
		return
	}

	s.publishSessionRewound(id, rev)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":      "coddy.session_rewound",
		"sessionId":   id,
		"messagesRev": rev,
	})
}
