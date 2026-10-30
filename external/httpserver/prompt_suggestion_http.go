//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Follow-up prompt suggestions over HTTP: the composer asks for the
// prediction of the operator's next message once its turn's stream closed,
// and reports what became of the suggestion it showed. The manager does the
// work (internal/session/prompt_suggestion.go); these routes are only the
// composer's door to it.

// promptSuggestionHTTPTimeout bounds the side call behind the GET: the
// composer is idle while it waits, so the bound is generous but finite.
const promptSuggestionHTTPTimeout = 25 * time.Second

// coddySessionPromptSuggestionGet answers the prediction for sessionID's next
// operator message. A turn with nothing to suggest - too little conversation,
// the model stayed silent, the gates said no - answers an empty text, not an
// error: the composer shows nothing either way.
func (s *Server) coddySessionPromptSuggestionGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}
	cfg := s.activeCfg()
	if cfg == nil || s.mgr == nil || !cfg.Agent.EffectivePromptSuggestions() {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.prompt_suggestion", "sessionId": id, "text": ""})
		return
	}
	if s.mgr.SessionByID(id) == nil {
		http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), promptSuggestionHTTPTimeout)
	defer cancel()
	text, err := s.mgr.GeneratePromptSuggestion(ctx, id)
	if err != nil && !session.IsPromptSuggestionSuppressed(err) {
		s.log.Error("prompt suggestion", "session", id, "error", err)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.prompt_suggestion", "sessionId": id, "text": text})
}

// coddySessionPromptSuggestionOutcomePost records what became of a shown
// suggestion: accepted (taken with Tab or click, or retyped by hand) resets
// the back-off streak, ignored grows it.
func (s *Server) coddySessionPromptSuggestionOutcomePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}
	var body struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	if s.mgr != nil {
		s.mgr.NotePromptSuggestionOutcome(id, body.Accepted)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.prompt_suggestion_outcome", "sessionId": id})
}
