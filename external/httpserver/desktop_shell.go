//go:build http && ui && desktop

package httpserver

import (
	"net/http"
	"sync"

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
	// The window is read once, on the first request, and the handler built
	// around it is kept.
	//
	// Not at mount time, because the SPA's catch-all is mounted while the
	// server is built and the window is handed over afterwards (Serve calls
	// SetDesktopWindow); a handler built here would hold the nil it saw. Not
	// per request either, because the handler keeps track of a drag of the
	// bar, and one rebuilt for every request would see every move of that
	// drag as the first. What is read is the launcher's handle, which exists
	// before the browser does and attaches the real window to itself a second
	// later, so one read is enough.
	var once sync.Once
	var inner http.Handler
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() {
			ctl, _ := s.desktopWindow.(shell.WindowControl)
			inner = shell.Handler(ctl)
		})
		inner.ServeHTTP(w, r)
	})
}

// servesDesktopShell reports whether this process is the one that opened a
// window. `coddy serve` never is, and must not answer /desktop/ at all: the
// page belongs to a window on loopback, and the shell's own routes are
// outside the authentication gate (they sit under the catch-all so that a
// browser with no cookie can reach the page). With no window they would all
// answer 501, which is not a reason to publish them on a networked server.
func (s *Server) servesDesktopShell() bool { return s.desktopWindow != nil }
