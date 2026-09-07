//go:build http

package httpserver

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// Provider usage over REST and on the server-wide events stream. The manager
// owns the cache and the schedule (internal/session/provider_usage.go); this
// file is the HTTP face: GET /coddy/providers/{name}/usage for the SPA, the
// remote console and scripts, and the provider_usage event on
// GET /coddy/events for updates produced outside a request (a finished turn,
// a deferred refresh). Both sit behind the same bearer policy as every other
// /coddy route, since the answer carries the account's wallet balance.

func (s *Server) registerProviderUsageRoutes() {
	s.mux.HandleFunc("GET /coddy/providers/{name}/usage", s.coddyProviderUsageGet)
}

// coddyProviderUsageGet answers {"ok":true,"usage":<update>} with the
// account usage behind a provider row; {"ok":false,"unsupported":true} for a
// provider type without a usage source, with "disabled":true added when the
// type has one but the row's panel is switched off
// (providers[].usage_limits_panel: false); {"ok":false,"error":...,"usage":<stale
// or null>} when the fetch failed, so a client keeps the last numbers; 404
// for an unknown provider name. ?refresh=1 asks for a fresh read (subject to
// the manager's pacing floor, which then answers the cached snapshot with
// refreshPending set).
func (s *Server) coddyProviderUsageGet(w http.ResponseWriter, r *http.Request) {
	c := s.activeCfg()
	if c == nil || s.mgr == nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "config unavailable")
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if c.FindProvider(name) == nil {
		writeCoddyConfigErr(w, http.StatusNotFound, "unknown provider")
		return
	}
	refresh := false
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("refresh"))) {
	case "1", "true", "yes":
		refresh = true
	}
	usage, err := s.mgr.ProviderUsage(r.Context(), name, refresh)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case err != nil:
		// A cancelled request or a fetch that produced nothing: the kind the
		// clients understand, with the detail beside it.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "unavailable", "detail": err.Error(), "usage": nil})
	case usage == nil:
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "no usage snapshot", "usage": nil})
	case usage.Unsupported:
		answer := map[string]interface{}{"ok": false, "unsupported": true, "provider": usage.Provider, "providerType": usage.ProviderType}
		if usage.Disabled {
			// The type has a source, the row's panel is switched off: a
			// client can tell the operator which switch to look at.
			answer["disabled"] = true
		}
		_ = json.NewEncoder(w).Encode(answer)
	case usage.Error != "":
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": usage.Error, "usage": usage})
	default:
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "usage": usage})
	}
}

// providerUsageFrame renders a usage snapshot as one SSE frame of the events
// stream: {"object":"coddy.provider_usage","sessionId":...,"usage":{...}}.
// sessionId names the session whose turn produced the snapshot ("" for a
// plain read); the snapshot itself is account-wide.
func providerUsageFrame(sessionID string, u acp.ProviderUsageUpdate) []byte {
	body, err := json.Marshal(map[string]interface{}{
		"object":    "coddy.provider_usage",
		"sessionId": sessionID,
		"usage":     u,
	})
	if err != nil {
		return nil
	}
	frame := make([]byte, 0, len(body)+40)
	frame = append(frame, "event: provider_usage\ndata: "...)
	frame = append(frame, body...)
	frame = append(frame, "\n\n"...)
	return frame
}

// publishProviderUsageEvent is the Manager usage observer this server
// registers in New.
func (s *Server) publishProviderUsageEvent(sessionID string, u acp.ProviderUsageUpdate) {
	if s.events == nil {
		return
	}
	if frame := providerUsageFrame(sessionID, u); frame != nil {
		s.events.publish(frame)
	}
}
