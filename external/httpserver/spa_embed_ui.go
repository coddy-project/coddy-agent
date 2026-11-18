//go:build http && ui

package httpserver

import (
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/ui"
)

// UIAvailable reports whether this binary embeds the web UI.
const UIAvailable = true

func mountEmbeddedSPARoot(s *Server) {
	spa := ui.Handler()
	// The desktop window's tab shell rides here rather than on a route of its
	// own: this catch-all is the one pattern the authentication gate leaves
	// open (isProtectedPattern), which is what lets a browser with no cookie
	// reach the page and sign in through it. The shell frames the SPA and
	// draws its titles from the gated session list, so nothing behind the gate
	// moves in front of it. Nil in a build without the surface, and silent on
	// a server that opened no window of its own.
	desktopShell := desktopShellHandler(s)
	s.mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ui.enable: false runs an API-only server even though the SPA is compiled in.
		if c := s.activeCfg(); c != nil && !c.UI.IsEnabled() {
			writeUIDisabledNotice(w)
			return
		}
		// Only the process that opened a window answers /desktop/: on
		// `coddy serve` the page belongs to nobody, and its routes sit
		// outside the authentication gate because the window's fresh profile
		// has no cookie to pass it with.
		if desktopShell != nil && s.servesDesktopShell() {
			if r.URL.Path == strings.TrimSuffix(desktopShellPrefix, "/") {
				http.Redirect(w, r, desktopShellPrefix, http.StatusMovedPermanently)
				return
			}
			if strings.HasPrefix(r.URL.Path, desktopShellPrefix) {
				desktopShell.ServeHTTP(w, r)
				return
			}
		}
		spa.ServeHTTP(w, r)
	}))
}

const uiDisabledResponse = "Coddy HTTP API is running with the embedded web UI disabled (ui.enable: false).\n"

func writeUIDisabledNotice(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(uiDisabledResponse))
}
