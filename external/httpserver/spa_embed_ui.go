//go:build http && ui

package httpserver

import (
	"net/http"

	"github.com/EvilFreelancer/coddy-agent/external/ui"
)

func mountEmbeddedSPARoot(s *Server) {
	spa := ui.Handler()
	s.mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ui.enabled: false runs an API-only server even though the SPA is compiled in.
		if c := s.activeCfg(); c != nil && !c.UI.IsEnabled() {
			writeUIDisabledNotice(w)
			return
		}
		spa.ServeHTTP(w, r)
	}))
}

const uiDisabledResponse = "Coddy HTTP API is running with the embedded web UI disabled (ui.enabled: false).\n"

func writeUIDisabledNotice(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(uiDisabledResponse))
}
