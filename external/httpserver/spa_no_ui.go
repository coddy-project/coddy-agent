//go:build http && !ui

package httpserver

import "net/http"

const spaNotEmbeddedResponse = "Coddy HTTP API is running without the embedded web UI (rebuild with -tags \"http ui\").\n"

func mountEmbeddedSPARoot(s *Server) {
	s.mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(spaNotEmbeddedResponse))
	}))
}
