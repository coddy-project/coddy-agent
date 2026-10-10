//go:build http

package httpserver

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
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
//
// And it says how much room the disk that stores the sessions has (storage):
// the state against sessions.min_free_mb, the figures of the disk and the
// threshold. The web UI polls it to warn before a save fails, and the events
// stream (event: storage_status) tells it at once when one does (issue #465).
// The object is absent when there is nothing to report - a manager that keeps
// no sessions on disk, a platform that cannot say and no failed save.
func (s *Server) coddyInfoGet(w http.ResponseWriter, _ *http.Request) {
	host, _ := os.Hostname()
	permissionMode := config.PermModeAsk
	if s.mgr != nil {
		permissionMode = s.mgr.DefaultPermissionMode()
	} else if cfg := s.activeCfg(); cfg != nil {
		permissionMode = cfg.Tools.ResolvedPermMode()
	}
	info := map[string]any{
		"object":         "coddy.info",
		"version":        version.Get(),
		"hostname":       host,
		"permissionMode": permissionMode,
	}
	if st, ok := s.readStorageStatus(); ok {
		info["storage"] = storageInfo(st)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

// readStorageStatus asks the manager how much room the disks have left (the
// override a test installed, when there is one).
func (s *Server) readStorageStatus() (session.StorageStatus, bool) {
	if s.storageStatus != nil {
		return s.storageStatus()
	}
	if s.mgr == nil {
		return session.StorageStatus{}, false
	}
	return s.mgr.StorageStatus()
}

// storageInfo is the storage object of GET /coddy/info. The figures are the
// disk that decides the state - the one in the worst state, the one with the
// least room among equals, and the sessions disk whenever a failed save is on
// record, since every such save went there - and are left out when that disk
// could not be read and the state comes from a failed write alone.
func storageInfo(st session.StorageStatus) map[string]any {
	out := map[string]any{
		"state":        string(st.State),
		"minFreeBytes": st.MinFreeBytes,
	}
	if st.Measured {
		out["volume"] = st.Volume
		out["freeBytes"] = st.FreeBytes
		out["totalBytes"] = st.TotalBytes
	}
	return out
}
