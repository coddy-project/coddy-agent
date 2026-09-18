//go:build http

package httpserver

// GET /coddy/mentions answers the "@" picker of the composer and of a console
// in remote mode: session.Manager.SearchMentions over the session's workspace,
// the folders typed so far, and the meta references. The same search serves
// the local console, so every surface offers the same candidates.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func (s *Server) coddyMentionsGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 0
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > session.MaxMentionLimit {
			http.Error(w, `{"error":{"message":"limit must be between 1 and 200"}}`, http.StatusBadRequest)
			return
		}
		limit = n
	}
	cwd, ok := s.resolveSessionCWD(w, r)
	if !ok {
		return
	}
	refresh := false
	switch strings.ToLower(strings.TrimSpace(q.Get("refresh"))) {
	case "1", "true", "yes":
		refresh = true
	}
	res, _ := s.mgr.SearchMentions(r.Context(), session.MentionSearch{
		SessionID: strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID")),
		CWD:       cwd,
		Query:     q.Get("q"),
		Limit:     limit,
		Refresh:   refresh,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":          "coddy.mentions",
		"items":           res.Items,
		"total":           res.Total,
		"indexing":        res.Indexing,
		"index_truncated": res.IndexTruncated,
	})
}
