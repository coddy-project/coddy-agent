//go:build http

package httpserver

// POST /coddy/sessions/{id}/compact: summarize older session history on
// demand. Mirrors the built-in /compact prompt command but returns the
// compaction outcome as JSON instead of an assistant message.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func (s *Server) coddySessionCompactPost(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}

	// An empty request body means "no extra instructions"; malformed JSON is an error.
	var body struct {
		Instructions string `json:"instructions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}

	st := s.mgr.SessionByID(id)
	if st == nil {
		fs := s.mgr.FileStore()
		if fs == nil || !fs.HasPersistedSnapshot(id) {
			http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
			return
		}
		if _, err := s.mgr.HandleSessionLoad(r.Context(), acp.SessionLoadParams{
			SessionID: id,
			CWD:       s.defaultCWD,
		}); err != nil {
			http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
			return
		}
		st = s.mgr.SessionByID(id)
		if st == nil {
			http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
			return
		}
	}

	unlock, err := s.mgr.AcquireComposerTurnLock(id, st)
	if err != nil {
		if errors.Is(err, session.ErrSessionTurnBusy) {
			http.Error(w, `{"error":{"message":"session busy: another agent turn is in progress"}}`, http.StatusConflict)
			return
		}
		s.log.Error("compact: turn lock", "session", id, "error", err)
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusInternalServerError)
		return
	}
	defer unlock()

	bridge := NewSender(s.activeCfg(), nil, false, st.GetMode())
	bridge.SetSessionDir(strings.TrimSpace(st.GetPersistedSessionDir()))
	ag := agent.NewAgent(s.activeCfg(), st, bridge, s.log)
	ag.SetProviderFactory(s.agentProviderFactory)

	res, err := ag.CompactSession(r.Context(), strings.TrimSpace(body.Instructions))
	w.Header().Set("Content-Type", "application/json")
	switch {
	case errors.Is(err, agent.ErrNothingToCompact):
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"compacted": false,
			"reason":    "nothing_to_compact",
		})
	case errors.Is(err, agent.ErrCompactionDisabled):
		http.Error(w, `{"error":{"message":"compaction is disabled (compaction.enabled)"}}`, http.StatusBadRequest)
	case err != nil:
		s.log.Error("compact: session compaction", "session", id, "error", err)
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusInternalServerError)
	default:
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"compacted":          true,
			"summary":            res.Summary,
			"compacted_messages": res.CompactedMessages,
			"kept_messages":      res.KeptMessages,
			"model":              res.Model,
		})
	}
}
