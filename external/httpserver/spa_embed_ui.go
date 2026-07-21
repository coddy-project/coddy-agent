//go:build http && ui

package httpserver

import (
	"net/http"

	"github.com/EvilFreelancer/coddy-agent/external/ui"
)

func mountEmbeddedSPARoot(s *Server) {
	spa := uiEmbeddedSPAHandler(http.FS(ui.Assets))
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

// uiEmbeddedSPAHandler serves the bundled SPA and sets Cache-Control on fixed asset paths
// so browsers revalidate after rebuilds (URLs have no content hash).
func uiEmbeddedSPAHandler(root http.FileSystem) http.Handler {
	next := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/index.html", "/app.js", "/styles.css",
			"/coddy-favicon.svg", "/favicon-32.png", "/favicon.ico", "/apple-touch-icon.png":
			w.Header().Set("Cache-Control", "no-cache")
		default:
		}
		next.ServeHTTP(w, r)
	})
}
