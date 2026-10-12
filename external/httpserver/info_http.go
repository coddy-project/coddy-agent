//go:build http

package httpserver

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// coddyInfoGet says which build serves this API and on which machine. The web
// UI shows the version in the start screen's footer, for the server the page
// is talking to - the local one, a remote or a node through a relay - and
// names the machine the page runs on by its host name on the swarm map, where
// the connection to a relay starts.
//
// It also names the permission mode a new session starts under: the one
// chosen last on any surface (session.Manager.DefaultPermissionMode, ask
// until one is chosen), the configuredPermissionMode of every settings
// snapshot. The start
// screen has no session to read a snapshot from, yet its permission chip has
// to show the mode the first turn will run under, and a mode picked there is
// sent with the first message only when it differs from this one. A session
// switched anywhere moves it, and the client hears of that through the
// snapshot event: session_settings carries it.
func (s *Server) coddyInfoGet(w http.ResponseWriter, _ *http.Request) {
	host, _ := os.Hostname()
	permissionMode := config.PermModeAsk
	if s.mgr != nil {
		permissionMode = s.mgr.DefaultPermissionMode()
	} else if cfg := s.activeCfg(); cfg != nil {
		permissionMode = cfg.Tools.ResolvedPermMode()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"object":         "coddy.info",
		"version":        version.Get(),
		"hostname":       host,
		"permissionMode": permissionMode,
	})
}
