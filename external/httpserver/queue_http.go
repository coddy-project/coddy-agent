//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// registerQueueRoutes wires the per-session message queue: what an operator
// writes while a turn is running, waiting for that turn to read it at its next
// step (docs/features/message-queue.md).
//
// The queue lives in process memory. Admission opens during a turn; an
// after_turn message may remain visible after Stop until it is removed or run.
func (s *Server) registerQueueRoutes() {
	s.mux.HandleFunc("GET /coddy/sessions/{id}/queue", s.coddyQueueList)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/queue", s.coddyQueuePost)
	s.mux.HandleFunc("DELETE /coddy/sessions/{id}/queue", s.coddyQueueClear)
	s.mux.HandleFunc("DELETE /coddy/sessions/{id}/queue/{message_id}", s.coddyQueueDelete)
	s.mux.HandleFunc("PATCH /coddy/sessions/{id}/queue/{message_id}", s.coddyQueuePatch)
}

// queueBody is what a client posts to add a follow-up.
type queueBody struct {
	Text        string             `json:"text"`
	Mode        session.QueueMode  `json:"mode,omitempty"`
	InlineFiles []acp.ImagePartRef `json:"inline_files,omitempty"`
}

// writeQueue answers with the queue as it now stands.
//
// The answer carries the same version the SSE frames do, so a client applying
// both keeps whichever is newer rather than letting a request that finished
// late overwrite a change it already heard about.
//
// message is the one message the request was about, when there is one: the
// row it added, or the row it took back (takenBack).
func writeQueue(w http.ResponseWriter, status int, sessionID string, st *session.State, message interface{}) {
	queue, version := st.QueueSnapshot()
	out := map[string]interface{}{
		"object":    "coddy.message_queue",
		"sessionId": sessionID,
		"messages":  session.QueuedMessagesWire(queue),
		"version":   version,
	}
	if message != nil {
		out["message"] = message
	}
	writeJSON(w, status, out)
}

// takenBackMessage is a message the operator took back, with everything it
// carried: inline_files holds its images in full, in the shape POST takes, so a
// client can put the message back into its draft exactly as it was written.
// Rows everywhere else name their images and never carry them.
type takenBackMessage struct {
	acp.QueuedMessage
	InlineFiles []acp.ImagePartRef `json:"inline_files,omitempty"`
}

func takenBack(q session.QueuedMessage) takenBackMessage {
	return takenBackMessage{QueuedMessage: q.Wire(), InlineFiles: q.ImageParts}
}

// queueInlineFiles checks the images a follow-up brings. They are kept in
// memory until the agent reads the message, then saved with the session's
// assets and sent to the model, so only an image carried in the request itself
// is taken: a base64 data URI of an image type, never a URL to fetch.
func queueInlineFiles(files []acp.ImagePartRef) ([]acp.ImagePartRef, error) {
	for i, f := range files {
		header, payload, ok := strings.Cut(f.DataURL, ",")
		h := strings.ToLower(header)
		if !ok || !strings.HasPrefix(h, "data:image/") || !strings.HasSuffix(h, ";base64") || payload == "" {
			return nil, fmt.Errorf("inline_files[%d]: data_url must be a base64 data URI of an image (data:image/...;base64,...)", i)
		}
	}
	return files, nil
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON body"}}`, http.StatusBadRequest)
		return
	}
	// Settings commands at the start of the text apply at once and never
	// reach the model; only the rest is queued (session.EnqueueFollowUp).
	if body.Mode == "" {
		body.Mode = session.QueueModeSteer
	}
	files, err := queueInlineFiles(body.InlineFiles)
	if err != nil {
		s.queueError(w, http.StatusBadRequest, "invalid_request", err)
		return
	}
	// Images reach the model only when it reads them - the rule POST
	// /v1/responses applies to an ordinary prompt.
	if cfg := s.activeCfg(); len(files) > 0 && !configuredModelMultimodal(cfg, effectiveYAMLModel(cfg, st)) {
		files = nil
	}
	msg, queued, notice, err := s.mgr.EnqueueFollowUpWithMode(r.Context(), id, body.Text, "web", body.Mode, files)
	switch {
	case errors.Is(err, session.ErrTurnScopedFollowUp):
		s.queueError(w, http.StatusConflict, "turn_scoped_follow_up", err)
		return
	case errors.Is(err, session.ErrNoActiveTurn):
		// 409 rather than 400: the request is well formed, the session is
		// simply not working right now. The client sends it as an ordinary
		// prompt instead, which is what the SPA and the console both do.
		s.queueError(w, http.StatusConflict, "no_active_turn", err)
		return
	case errors.Is(err, session.ErrQueueFull):
		s.queueError(w, http.StatusConflict, "queue_full", err)
		return
	case isSubagentReadOnly(err):
		s.queueError(w, http.StatusConflict, "subagent_read_only", err)
		return
	case err != nil:
		s.queueError(w, http.StatusBadRequest, "invalid_request", err)
		return
	}
	if !queued {
		// Only settings commands: nothing waits for the turn, the notice
		// says what changed.
		out := map[string]interface{}{
			"object":    "coddy.message_queue",
			"sessionId": id,
			"notice":    notice,
		}
		queue, version := st.QueueSnapshot()
		out["messages"] = session.QueuedMessagesWire(queue)
		out["version"] = version
		if snap, err := s.mgr.SessionSettings(id); err == nil {
			out["settings"] = snap
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	writeQueue(w, http.StatusCreated, id, st, msg.Wire())
}

func (s *Server) coddyQueuePatch(w http.ResponseWriter, r *http.Request) {
	id, st := s.queueSession(w, r)
	if st == nil {
		return
	}
	var body struct {
		Mode session.QueueMode `json:"mode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || !session.ValidQueueMode(body.Mode) {
		s.queueError(w, http.StatusBadRequest, "invalid_request", fmt.Errorf("mode must be steer or after_turn"))
		return
	}
	_, err := s.mgr.SetQueuedTurnMessageMode(id, strings.TrimSpace(r.PathValue("message_id")), body.Mode)
	if errors.Is(err, session.ErrQueuedMessageNotFound) {
		s.queueError(w, http.StatusNotFound, "not_found", err)
		return
	}
	if err != nil {
		s.queueError(w, http.StatusBadRequest, "invalid_request", err)
		return
	}
	writeQueue(w, http.StatusOK, id, st, nil)
}

func (s *Server) coddyQueueDelete(w http.ResponseWriter, r *http.Request) {
	id, st := s.queueSession(w, r)
	if st == nil {
		return
	}
	messageID := strings.TrimSpace(r.PathValue("message_id"))
	taken, _, err := s.mgr.TakeBackQueuedTurnMessage(id, messageID)
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
	writeQueue(w, http.StatusOK, id, st, takenBack(taken))
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
