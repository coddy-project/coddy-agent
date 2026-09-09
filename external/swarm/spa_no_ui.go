//go:build swarm && !ui

package swarm

import "net/http"

// mountSPARoot without the ui tag: the relay has no SPA to serve, and says so
// rather than answering a bare 404 that reads like a broken address.
func mountSPARoot(s *Server) {
	s.mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSPANotice(w, spaNotEmbeddedResponse)
	}))
}

const spaNotEmbeddedResponse = "Coddy swarm relay is running without the embedded web UI (rebuild with -tags \"swarm ui\").\n"
