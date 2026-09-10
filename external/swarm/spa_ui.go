//go:build swarm && ui

package swarm

import (
	"net/http"

	"github.com/EvilFreelancer/coddy-agent/external/ui"
)

// mountSPARoot puts the bundled SPA on the relay's own root, so a relay is
// something you open in a browser rather than a service you have to reach
// through some other node's UI. The SPA discovers it is a relay through
// GET /swarm/info and shows the swarm map instead of a chat.
func mountSPARoot(s *Server) {
	spa := ui.Handler()
	s.mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.UI.IsEnabled() {
			writeSPANotice(w, uiDisabledResponse)
			return
		}
		spa.ServeHTTP(w, r)
	}))
}

const uiDisabledResponse = "Coddy swarm relay is running with the embedded web UI disabled (ui.enable: false).\n"
