//go:build swarm

package swarm

import "net/http"

// handleStats answers GET /swarm/stats with the counters of the relay. It is the full class only (a scoped client gets the gate's plain 401)
// and it is not in the prefixes a mount carries, so a parent cannot read a child's.
func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.stats.snapshot())
}
