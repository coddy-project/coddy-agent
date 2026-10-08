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
// It also names the permission mode a new session starts under: the live
// configuration's tools.permission_mode, resolved the way a session's settings
// snapshot resolves it (ask when the configuration names none). The start
// screen has no session to read a snapshot from, yet its permission chip has
// to show the mode the first turn will run under, and a mode picked there is
// sent with the first message only when it differs from this one. The answer
// follows every reload, which the client hears of as event: config_reloaded.
func (s *Server) coddyInfoGet(w http.ResponseWriter, _ *http.Request) {
	host, _ := os.Hostname()
	permissionMode := config.PermModeAsk
	if cfg := s.activeCfg(); cfg != nil {
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
