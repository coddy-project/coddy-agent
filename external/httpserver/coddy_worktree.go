//go:build http

package httpserver

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// worktreeSessionDTO is another live session that works in the worktree.
type worktreeSessionDTO struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

// registerWorktreeRoutes adds what the web UI reads before it asks whether to
// keep a chat's worktree or remove it.
func (s *Server) registerWorktreeRoutes() {
	s.mux.HandleFunc("GET /coddy/sessions/{id}/worktree", s.coddySessionWorktree)
}

// coddySessionWorktree handles GET /coddy/sessions/{id}/worktree: the linked
// worktree the session works in and what removing it would touch, with the
// refusal worktree_exit and /worktree exit --remove would give
// (tools.InspectSessionWorktree). It reads and changes nothing.
func (s *Server) coddySessionWorktree(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	_, cwd := s.sessionWorkspace(w, r, id)
	if cwd == "" {
		return
	}
	env := &tools.Env{
		CWD:        cwd,
		SessionID:  id,
		Background: bgtask.Default(),
		SessionsInWorkspace: func(dir string) []string {
			return s.mgr.SessionsInWorkspace(dir, id)
		},
	}
	inspection, err := tools.InspectSessionWorktree(env)
	if err != nil {
		s.log.Error("inspect the session's worktree", "session", id, "error", err)
		writeSubagentsError(w, http.StatusInternalServerError, "inspect failed")
		return
	}
	others := make([]worktreeSessionDTO, 0, len(inspection.OtherSessions))
	for _, other := range inspection.OtherSessions {
		dto := worktreeSessionDTO{ID: other}
		if st := s.mgr.SessionByID(other); st != nil {
			dto.Title = st.ConversationTitle()
		}
		others = append(others, dto)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":         "coddy.session_worktree",
		"sessionId":      id,
		"in_worktree":    inspection.InWorktree,
		"path":           inspection.Path,
		"branch":         inspection.Branch,
		"main_checkout":  inspection.MainCheckout,
		"managed":        inspection.Managed,
		"uncommitted":    inspection.Uncommitted,
		"unpushed":       inspection.Unpushed,
		"loose_commit":   inspection.LooseCommit,
		"locked":         inspection.Locked,
		"submodules":     inspection.Submodules,
		"other_sessions": others,
		"running_tasks":  inspection.RunningTasks,
		"removable":      inspection.Removable,
		"needs_discard":  inspection.NeedsDiscard,
		"refusal":        inspection.Refusal,
	})
}
