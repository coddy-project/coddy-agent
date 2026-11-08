//go:build http && ui && desktop

package httpserver

import (
	"net/http"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
)

// desktopShellPrefix is where the tab shell of the desktop window answers.
const desktopShellPrefix = shell.Prefix

// desktopShellHandler is the shell this binary serves, or nil when it was not
// built with the desktop surface. It is mounted inside the "/" catch-all
// rather than as a route of its own, because isProtectedPattern exempts "/"
// and protects everything registered: a shell behind the gate would answer 401
// to the window's fresh profile, which has no cookie to sign in with yet.
// The window is resolved per request rather than captured here: the SPA's
// catch-all is mounted while the server is built, and the window it drives is
// handed over later - by Serve, and attached to by the launcher a second after
// that, once the browser has actually mapped a window. A handler that read the
// field once would hold the nil it saw at mount time, and the strip would
// never draw its buttons.
func desktopShellHandler(s *Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctl, _ := s.desktopWindow.(shell.WindowControl)
		shell.Handler(ctl).ServeHTTP(w, r)
	})
}
