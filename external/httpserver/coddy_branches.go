//go:build http

package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// captureAndStoreTurnDiff asynchronously computes the workspace diff against the
// pre-turn snapshot and stores it in the session directory.
// It runs in a goroutine to avoid blocking the HTTP response.
func (s *Server) captureAndStoreTurnDiff(st *session.State, before *session.WorkspaceSnapshot) {
	sd := strings.TrimSpace(st.GetPersistedSessionDir())
	cwd := strings.TrimSpace(st.GetCWD())
	if sd == "" || cwd == "" {
		return
	}
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		turnN := session.TurnNumber(st.GetMessages())
		diff, err := session.ComputeWorkspaceDiff(cwd, before)
		if err != nil {
			s.log.Warn("compute workspace diff", "turn", turnN, "error", err)
			return
		}
		if err := session.StoreWorkspaceDiff(sd, turnN, diff); err != nil {
			s.log.Warn("store workspace diff", "turn", turnN, "error", err)
		}
	}()
}

// registerBranchRoutes adds branching endpoints to the server.
func (s *Server) registerBranchRoutes() {
	s.mux.HandleFunc("POST /coddy/sessions/{id}/branches", s.coddyBranchCreate)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/branches", s.coddyBranchList)
}

// coddyBranchCreate handles POST /coddy/sessions/{id}/branches.
//
// Request body:
//
//	{ "userMessageIndex": <int> }
//
// Response:
//
//	{ "object": "coddy.branch_created", "newSessionId": "...", "branchIndex": N, "totalBranches": M,
//	  "fileRollbackNote": "..." }
func (s *Server) coddyBranchCreate(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}

	var body struct {
		UserMessageIndex int `json:"userMessageIndex"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	if body.UserMessageIndex < 0 {
		http.Error(w, `{"error":{"message":"userMessageIndex must be >= 0"}}`, http.StatusBadRequest)
		return
	}

	fs := s.coddyRequireStore(w)
	if fs == nil {
		return
	}
	if !fs.HasPersistedSnapshot(id) {
		http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
		return
	}

	// Determine the CWD of the source session (for file rollback).
	var cwd string
	if st := s.mgr.SessionByID(id); st != nil {
		cwd = st.GetCWD()
	} else if snap, err := fs.ReadSnapshot(id); err == nil {
		cwd = snap.Meta.CWD
	}

	// Reverse workspace diffs for turns after the branch point.
	fileNote := ""
	if cwd != "" {
		srcDir := fs.SessionPath(id)
		if note, err := session.RestoreWorkspaceFiles(cwd, srcDir, body.UserMessageIndex); err == nil {
			fileNote = note
		} else {
			fileNote = "file rollback error: " + err.Error()
		}
	}

	result, err := s.mgr.CreateBranchSession(session.CreateBranchParams{
		SourceSessionID:  id,
		UserMessageIndex: body.UserMessageIndex,
	})
	if err != nil {
		s.log.Error("create branch session", "source", id, "error", err)
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":           "coddy.branch_created",
		"newSessionId":     result.NewSessionID,
		"branchIndex":      result.BranchIndex,
		"totalBranches":    result.TotalBranches,
		"fileRollbackNote": fileNote,
	})
}

// coddyBranchList handles GET /coddy/sessions/{id}/branches.
//
// Response:
//
//	{ "object": "coddy.branches", "sessionId": "...", "branchPoints": [...] }
func (s *Server) coddyBranchList(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}

	fs := s.coddyRequireStore(w)
	if fs == nil {
		return
	}
	if !fs.HasPersistedSnapshot(id) {
		http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
		return
	}

	views, err := s.mgr.LoadBranchPointViews(id)
	if err != nil {
		s.log.Error("load branch views", "session", id, "error", err)
		http.Error(w, `{"error":{"message":"read failed"}}`, http.StatusInternalServerError)
		return
	}
	if views == nil {
		views = []session.BranchPointView{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":       "coddy.branches",
		"sessionId":    id,
		"branchPoints": views,
	})
}
