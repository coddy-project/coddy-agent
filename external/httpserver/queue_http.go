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

// registerQueueRoutes wires the per-session message queue: what an operator
// writes while a turn is running, waiting for that turn to read it at its next
// step (docs/features/message-queue.md).
//
// The queue belongs to the turn, not to the session bundle: it is opened when a
// turn is admitted and gone when that turn releases, so every route here
// answers about a session that is working right now.
func (s *Server) registerQueueRoutes() {
	s.mux.HandleFunc("GET /coddy/sessions/{id}/queue", s.coddyQueueList)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/queue", s.coddyQueuePost)
	s.mux.HandleFunc("DELETE /coddy/sessions/{id}/queue", s.coddyQueueClear)
	s.mux.HandleFunc("DELETE /coddy/sessions/{id}/queue/{message_id}", s.coddyQueueDelete)
}

// queueBody is what a client posts to add a follow-up.
type queueBody struct {
	Text string `json:"text"`
}

// writeQueue answers with the queue as it now stands.
//
// The answer carries the same version the SSE frames do, so a client applying
// both keeps whichever is newer rather than letting a request that finished
// late overwrite a change it already heard about.
func writeQueue(w http.ResponseWriter, status int, sessionID string, st *session.State, added *session.QueuedMessage) {
	queue, version := st.QueueSnapshot()
	out := map[string]interface{}{
		"object":    "coddy.message_queue",
		"sessionId": sessionID,
		"messages":  session.QueuedMessagesWire(queue),
		"version":   version,
	}
	if added != nil {
		out["message"] = added.Wire()
	}
	writeJSON(w, status, out)
}

// queueSession resolves the session a queue route names, loading a persisted
// bundle the way every other /coddy route does.
func (s *Server) queueSession(w http.ResponseWriter, r *http.Request) (string, *session.State) {
	id := strings.TrimSpace(r.PathValue("id"))
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return "", nil
	}
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return "", nil
	}
	return id, st
}

func (s *Server) coddyQueueList(w http.ResponseWriter, r *http.Request) {
	id, st := s.queueSession(w, r)
	if st == nil {
		return
	}
	writeQueue(w, http.StatusOK, id, st, nil)
}

func (s *Server) coddyQueuePost(w http.ResponseWriter, r *http.Request) {
	id, st := s.queueSession(w, r)
	if st == nil {
		return
	}
	var body queueBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON body"}}`, http.StatusBadRequest)
		return
	}
	msg, _, err := s.mgr.EnqueueTurnMessage(id, body.Text)
	switch {
	case errors.Is(err, session.ErrNoActiveTurn):
		// 409 rather than 400: the request is well formed, the session is
		// simply not working right now. The client sends it as an ordinary
		// prompt instead, which is what the SPA and the console both do.
		s.queueError(w, http.StatusConflict, "no_active_turn", err)
		return
	case errors.Is(err, session.ErrQueueFull):
		s.queueError(w, http.StatusConflict, "queue_full", err)
		return
	case errors.Is(err, session.ErrSubagentReadOnly):
		s.queueError(w, http.StatusConflict, "subagent_read_only", err)
		return
	case err != nil:
		s.queueError(w, http.StatusBadRequest, "invalid_request", err)
		return
	}
	writeQueue(w, http.StatusCreated, id, st, &msg)
}

func (s *Server) coddyQueueDelete(w http.ResponseWriter, r *http.Request) {
	id, st := s.queueSession(w, r)
	if st == nil {
		return
	}
	messageID := strings.TrimSpace(r.PathValue("message_id"))
	_, err := s.mgr.CancelQueuedTurnMessage(id, messageID)
	switch {
	case errors.Is(err, session.ErrQueuedMessageNotFound):
		// Losing the race with the agent is the ordinary way this happens: the
		// message was read a moment ago and is now part of the conversation.
		s.queueError(w, http.StatusNotFound, "not_found", err)
		return
	case err != nil:
		s.queueError(w, http.StatusBadRequest, "invalid_request", err)
		return
	}
	writeQueue(w, http.StatusOK, id, st, nil)
}

func (s *Server) coddyQueueClear(w http.ResponseWriter, r *http.Request) {
	id, st := s.queueSession(w, r)
	if st == nil {
		return
	}
	if err := s.mgr.ClearQueuedTurnMessages(id); err != nil {
		s.queueError(w, http.StatusBadRequest, "invalid_request", err)
		return
	}
	writeQueue(w, http.StatusOK, id, st, nil)
}

// queueError answers in the error shape the rest of /coddy uses, with a code a
// client can branch on rather than matching prose.
func (s *Server) queueError(w http.ResponseWriter, status int, code string, err error) {
	writeJSON(w, status, map[string]interface{}{
		"error": map[string]interface{}{
			"message": err.Error(),
			"code":    code,
		},
	})
}
