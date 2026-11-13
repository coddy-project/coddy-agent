//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
)

// workspaceGraphTimeout bounds a history read the way the branch refresh is
// bounded: --topo-order walks the whole graph for every page, and a read must
// not hold a handler for good on a huge repository.
var workspaceGraphTimeout = 20 * time.Second

// coddyWorkspaceBranchesPost serves the start screen, before a session exists.
// Session clients use POST /sessions/{id}/workspace and its workspace lock.
func (s *Server) coddyWorkspaceBranchesPost(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID")) != "" {
		http.Error(w, errorJSON("use the session workspace endpoint to create a branch"), http.StatusBadRequest)
		return
	}
	cwd, ok := s.workspaceContextCWD(w, r)
	if !ok {
		return
	}
	var body struct {
		Branch string `json:"branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, errorJSON("invalid JSON"), http.StatusBadRequest)
		return
	}
	if err := gitws.CreateAndSwitch(cwd, body.Branch); err != nil {
		http.Error(w, errorJSON(err.Error()), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(workspaceContextPayload(cwd))
}

func (s *Server) coddyWorkspaceGraphGet(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.workspaceContextCWD(w, r)
	if !ok {
		return
	}
	offset, limit := 0, 100
	for key, dest := range map[string]*int{"offset": &offset, "limit": &limit} {
		if raw := r.URL.Query().Get(key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil {
				http.Error(w, errorJSON("invalid "+key), http.StatusBadRequest)
				return
			}
			*dest = n
		}
	}
	if offset < 0 || offset > 100000 || limit < 1 || limit > 500 {
		http.Error(w, errorJSON("invalid history page"), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), workspaceGraphTimeout)
	defer cancel()
	page, err := gitws.History(ctx, cwd, offset, limit)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, errorJSON("reading the git history timed out"), http.StatusBadRequest)
			return
		}
		http.Error(w, errorJSON(err.Error()), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}
