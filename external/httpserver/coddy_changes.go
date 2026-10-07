//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/linediff"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/textenc"
)

// changedFileDTO is one file of the session's working copy changes. Patch,
// Before and After are pointers so an empty string still serialises: a binary
// file legitimately has an empty patch, and the caller must be able to tell
// that apart from a field it did not ask for.
type changedFileDTO struct {
	Path      string  `json:"path"`
	Status    string  `json:"status"`
	Additions int     `json:"additions"`
	Deletions int     `json:"deletions"`
	Binary    bool    `json:"binary"`
	Truncated bool    `json:"truncated"`
	Patch     *string `json:"patch,omitempty"`
	Before    *string `json:"before,omitempty"`
	After     *string `json:"after,omitempty"`
}

// registerChangesRoutes adds the routes behind a chat's Edits view: what git
// reports for the session's folder, one file of it, and discarding it.
func (s *Server) registerChangesRoutes() {
	s.mux.HandleFunc("GET /coddy/sessions/{id}/changes", s.coddySessionChangesList)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/changes/file", s.coddySessionChangeFile)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/changes/revert", s.coddySessionChangesRevert)
}

// decodeSide turns file bytes into text. textenc owns the encoding decision for
// the whole tree, so a Windows-1251 source reads the same here as it does
// through the file tools. Undecodable content is reported as binary.
func decodeSide(data []byte) (text string, binary bool) {
	if data == nil {
		return "", false
	}
	if textenc.LooksBinary(data) {
		return "", true
	}
	decoded, _, err := textenc.DecodeToUTF8(data)
	if err != nil {
		return "", true
	}
	return decoded, false
}

// buildChangedFile renders one working copy change into its wire form.
func buildChangedFile(change gitws.WorkChange, withPatch, withContent bool) changedFileDTO {
	before, beforeBinary := decodeSide(change.Before)
	after, afterBinary := decodeSide(change.After)
	binary := beforeBinary || afterBinary

	dto := changedFileDTO{Path: change.Path, Status: change.Status, Binary: binary}
	if !binary {
		dto.Additions, dto.Deletions = linediff.Stat(before, after)
	}
	if withPatch {
		patch := ""
		if !binary {
			var truncated bool
			patch, truncated = linediff.Unified(change.Path, before, after, linediff.DefaultContext)
			dto.Truncated = truncated
		}
		dto.Patch = &patch
	}
	if withContent {
		dto.Before = &before
		dto.After = &after
	}
	return dto
}

// sessionWorkspace resolves the folder a session works in, writing the HTTP
// error itself when the session is unknown or has none.
func (s *Server) sessionWorkspace(w http.ResponseWriter, r *http.Request, id string) (*session.State, string) {
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return nil, ""
	}
	cwd := strings.TrimSpace(st.GetCWD())
	if cwd == "" {
		writeSubagentsError(w, http.StatusBadRequest, "session has no workspace")
		return nil, ""
	}
	return st, cwd
}

// parseChangeIncludes reads the comma-separated `include` query parameter.
func parseChangeIncludes(raw string) (withPatch, withContent bool) {
	for _, part := range strings.Split(raw, ",") {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "patch":
			withPatch = true
		case "content":
			withContent = true
		}
	}
	return withPatch, withContent
}

// coddySessionChangesList handles GET /coddy/sessions/{id}/changes.
//
// Response:
//
//	{ "object": "coddy.session_changes", "sessionId": "...", "vcs": "git",
//	  "files": [ { "path": "...", "status": "modified", "additions": 2, "deletions": 1,
//	               "binary": false, "truncated": false } ],
//	  "totals": { "files": 1, "additions": 2, "deletions": 1 },
//	  "skipped": 0 }
//
// The set is git's report of the session's folder: tracked files that differ
// from HEAD and new files git does not ignore. `vcs` is empty when the folder
// is not inside a repository, and the set is then empty. `skipped` counts new
// files left out (past the cap, or too large to read). `?include=patch,content`
// adds the unified patch and the decoded file sides.
func (s *Server) coddySessionChangesList(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	_, cwd := s.sessionWorkspace(w, r, id)
	if cwd == "" {
		return
	}
	vcs := ""
	var work []gitws.WorkChange
	skipped := 0
	if gitws.Describe(cwd).IsGitRepo {
		vcs = "git"
		var err error
		work, skipped, err = gitws.WorktreeChanges(cwd)
		if err != nil {
			s.log.Error("read working copy changes", "session", id, "error", err)
			writeSubagentsError(w, http.StatusInternalServerError, "read failed")
			return
		}
	}

	withPatch, withContent := parseChangeIncludes(r.URL.Query().Get("include"))
	files := make([]changedFileDTO, 0, len(work))
	totalAdd, totalDel := 0, 0
	for _, change := range work {
		dto := buildChangedFile(change, withPatch, withContent)
		totalAdd += dto.Additions
		totalDel += dto.Deletions
		files = append(files, dto)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":    "coddy.session_changes",
		"sessionId": id,
		"vcs":       vcs,
		"files":     files,
		"totals": map[string]int{
			"files":     len(files),
			"additions": totalAdd,
			"deletions": totalDel,
		},
		"skipped": skipped,
	})
}

// coddySessionChangeFile handles GET /coddy/sessions/{id}/changes/file?path=...
//
// The path is matched against what git reports rather than resolved on disk,
// so this route cannot be pointed at a file outside the change set. Only the
// wanted file is read: the review window asks a file at a time, and reading
// every changed blob per request would be a git process per file on each one.
func (s *Server) coddySessionChangeFile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	path := r.URL.Query().Get("path")
	if strings.TrimSpace(path) == "" {
		writeSubagentsError(w, http.StatusBadRequest, "path is required")
		return
	}
	_, cwd := s.sessionWorkspace(w, r, id)
	if cwd == "" {
		return
	}
	// The exact name git listed first: on a POSIX system a backslash is a
	// character of a file name. Then the other separator, since the list is
	// OS-shaped and a caller may send either form of a Windows path.
	var change *gitws.WorkChange
	if gitws.Describe(cwd).IsGitRepo {
		for _, want := range []string{path, filepath.FromSlash(strings.ReplaceAll(path, `\`, "/"))} {
			var err error
			change, err = gitws.WorktreeChangeFor(cwd, want)
			if err != nil {
				s.log.Error("read working copy change", "session", id, "error", err)
				writeSubagentsError(w, http.StatusInternalServerError, "read failed")
				return
			}
			if change != nil {
				break
			}
		}
	}
	if change == nil {
		writeSubagentsError(w, http.StatusNotFound, "file is not among the working copy changes")
		return
	}
	dto := buildChangedFile(*change, true, true)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":    "coddy.session_change",
		"sessionId": id,
		"path":      dto.Path,
		"status":    dto.Status,
		"additions": dto.Additions,
		"deletions": dto.Deletions,
		"binary":    dto.Binary,
		"truncated": dto.Truncated,
		"patch":     dto.Patch,
		"before":    dto.Before,
		"after":     dto.After,
	})
}

// discardRequest is the body of a discard: the files to put back at HEAD, or
// all of them. It never defaults to everything - an empty body, both keys, an
// empty list or an unknown key is refused - because the call deletes the new
// files it covers.
type discardRequest struct {
	// Raw, so a "paths" that is there but null or empty is told apart from
	// one that is not there at all.
	Paths json.RawMessage `json:"paths"`
	All   bool            `json:"all"`
}

// coddySessionChangesRevert handles POST /coddy/sessions/{id}/changes/revert.
//
// It discards uncommitted changes of the session's folder through git: a
// tracked file gets HEAD's content and index entry back, a file HEAD does not
// hold is deleted. Files git ignores are never touched.
func (s *Server) coddySessionChangesRevert(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	// Exactly one JSON object: whatever follows it would make a destructive
	// request mean something other than what its first half says.
	var req discardRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		writeSubagentsError(w, http.StatusBadRequest, `body must be {"paths":[...]} or {"all":true}`)
		return
	}
	var paths []string
	if len(req.Paths) > 0 {
		if err := json.Unmarshal(req.Paths, &paths); err != nil || len(paths) == 0 {
			writeSubagentsError(w, http.StatusBadRequest, `"paths" must name at least one file`)
			return
		}
	}
	if req.All == (len(req.Paths) > 0) {
		writeSubagentsError(w, http.StatusBadRequest, `name the files in "paths" or set "all", not both and not neither`)
		return
	}
	st, cwd := s.sessionWorkspace(w, r, id)
	if cwd == "" {
		return
	}
	// Rewriting files under an agent that is mid-edit would leave the folder in
	// a state neither side expects - this chat's agent, or that of any other
	// chat working in the same folder, or in one inside or around it.
	if s.sessionTurnActive(id) {
		writeSubagentsError(w, http.StatusConflict, "session "+id+" has a turn in flight")
		return
	}
	if busy := s.turnInFolder(id, cwd); busy != "" {
		writeSubagentsError(w, http.StatusConflict, "session "+busy+" has a turn in flight in this folder")
		return
	}
	unlock, err := s.mgr.AcquireComposerTurnLock(id, st)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, session.ErrSessionTurnBusy) {
			status = http.StatusConflict
		}
		writeSubagentsError(w, status, err.Error())
		return
	}
	defer unlock()

	if err := gitws.Discard(cwd, paths); err != nil {
		switch {
		case errors.Is(err, gitws.ErrNotRepository):
			writeSubagentsError(w, http.StatusBadRequest, "the session's folder is not a git repository")
		case errors.Is(err, gitws.ErrNotChanged):
			// The client asked for a file its list showed and git no longer
			// reports: committed, or put back, since it last read.
			writeSubagentsError(w, http.StatusConflict, err.Error())
		default:
			s.log.Error("discard working copy changes", "session", id, "error", err)
			// A failure halfway may have put some files back already, so
			// every window reads the folder again.
			s.publishSessionChanges(id)
			writeSubagentsError(w, http.StatusInternalServerError, "discard failed")
		}
		return
	}
	// Another window showing this session reads the new state as well.
	s.publishSessionChanges(id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":    "coddy.session_changes_reverted",
		"sessionId": id,
		"at":        time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// turnInFolder names another session of this process with a turn running in
// cwd, in a folder inside it or in one around it, or returns "". A discard
// rewrites the folder, so their agents would find their files changed under
// them.
func (s *Server) turnInFolder(self, cwd string) string {
	for _, other := range s.mgr.ActiveTurnSessionIDs() {
		if other == self {
			continue
		}
		st := s.mgr.SessionByID(other)
		if st == nil {
			continue
		}
		if foldersOverlap(cwd, st.GetCWD()) {
			return other
		}
	}
	return ""
}

// foldersOverlap reports whether one folder is the other or lies inside it,
// compared in their canonical form (links resolved, case folded where the
// filesystem does).
func foldersOverlap(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	if session.SameWorkspacePath(a, b) {
		return true
	}
	ca, cb := session.CanonicalWorkspacePath(a), session.CanonicalWorkspacePath(b)
	inside := func(root, p string) bool {
		rel, err := filepath.Rel(root, p)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	}
	return inside(ca, cb) || inside(cb, ca)
}
