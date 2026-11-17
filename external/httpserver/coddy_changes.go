//go:build http

package httpserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
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
// that apart from a field it did not ask for. Digest, Image and the two sizes
// describe the raw sides, so a viewer can fingerprint, size and render the
// file without fetching its content; the beforeImage and afterImage pair
// breaks the image flag down per side, since a file may change its kind.
type changedFileDTO struct {
	Path        string  `json:"path"`
	Status      string  `json:"status"`
	Additions   int     `json:"additions"`
	Deletions   int     `json:"deletions"`
	Binary      bool    `json:"binary"`
	Truncated   bool    `json:"truncated"`
	Digest      string  `json:"digest"`
	Image       bool    `json:"image"`
	BeforeImage bool    `json:"beforeImage"`
	AfterImage  bool    `json:"afterImage"`
	BeforeSize  int     `json:"beforeSize"`
	AfterSize   int     `json:"afterSize"`
	Patch       *string `json:"patch,omitempty"`
	Before      *string `json:"before,omitempty"`
	After       *string `json:"after,omitempty"`
}

// registerChangesRoutes adds the routes behind a chat's Edits view: what git
// reports for the session's folder, one file of it, and discarding it.
func (s *Server) registerChangesRoutes() {
	s.mux.HandleFunc("GET /coddy/sessions/{id}/changes", s.coddySessionChangesList)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/changes/file", s.coddySessionChangeFile)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/changes/revert", s.coddySessionChangesRevert)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/changes/delete", s.coddySessionChangesDelete)
}

// decodeSide turns file bytes into LF text. textenc owns the encoding decision
// for the whole tree, so a Windows-1251 source reads the same here as it does
// through the file tools. Undecodable content is reported as binary. The line
// endings are levelled after decoding, so the patch and the sides describe
// content rather than platform - the same view the sides gave when they were
// normalised upstream; the raw bytes stay in the WorkChange for everything
// else (the digest, the sizes, the raw image serving).
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
	return strings.ReplaceAll(decoded, "\r\n", "\n"), false
}

// changeDigest fingerprints what the row stands for: the status and both raw
// sides, so identical content hashes the same on every read and a rewrite that
// keeps the line counts still moves it. The sides are length-prefixed so no
// pair of contents can collide across the before/after boundary.
func changeDigest(change gitws.WorkChange) string {
	h := sha256.New()
	_, _ = h.Write([]byte(change.Status))
	var lenBuf [8]byte
	for _, side := range [][]byte{change.Before, change.After} {
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(side)))
		_, _ = h.Write(lenBuf[:])
		_, _ = h.Write(side)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// buildChangedFile renders one working copy change into its wire form.
func buildChangedFile(change gitws.WorkChange, withPatch, withContent bool) changedFileDTO {
	before, beforeBinary := decodeSide(change.Before)
	after, afterBinary := decodeSide(change.After)
	beforeImage := change.Before != nil && gitws.ImageMIME(change.Before) != ""
	afterImage := change.After != nil && gitws.ImageMIME(change.After) != ""
	// The row is binary when a side reads as binary or carries a recognized
	// image: an image can be plain ASCII and still have no line diff to show.
	binary := beforeBinary || afterBinary || beforeImage || afterImage

	dto := changedFileDTO{
		Path:        change.Path,
		Status:      change.Status,
		Binary:      binary,
		Digest:      changeDigest(change),
		Image:       beforeImage || afterImage,
		BeforeImage: beforeImage,
		AfterImage:  afterImage,
		BeforeSize:  len(change.Before),
		AfterSize:   len(change.After),
	}

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
	if gitws.IsRepo(cwd) {
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
//
// With raw=1 the route serves the exact bytes of one side instead of the JSON
// document - how the viewer renders an image, since the decoded content
// fields cannot carry binary data. side=before|after picks the side (before
// by default), the Content-Type is the raster format the bytes carry, a side
// the file does not have is 404, and a side that is not a supported image is
// 400: the raw form exists for images alone. The side parameter is read only
// for raw=1; without it the JSON answer ignores it entirely.
func (s *Server) coddySessionChangeFile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	path := r.URL.Query().Get("path")
	if strings.TrimSpace(path) == "" {
		writeSubagentsError(w, http.StatusBadRequest, "path is required")
		return
	}
	raw := r.URL.Query().Get("raw") == "1"
	_, cwd := s.sessionWorkspace(w, r, id)
	if cwd == "" {
		return
	}
	// The exact name git listed first: on a POSIX system a backslash is a
	// character of a file name. Then the other separator, since the list is
	// OS-shaped and a caller may send either form of a Windows path.
	var change *gitws.WorkChange
	if gitws.IsRepo(cwd) {
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
	if raw {
		side := r.URL.Query().Get("side")
		if side == "" {
			side = "before"
		}
		if side != "before" && side != "after" {
			writeSubagentsError(w, http.StatusBadRequest, `side must be "before" or "after"`)
			return
		}
		data := change.Before
		if side == "after" {
			data = change.After
		}
		if data == nil {
			writeSubagentsError(w, http.StatusNotFound, "file has no "+side+" side")
			return
		}
		mime := gitws.ImageMIME(data)
		if mime == "" {
			writeSubagentsError(w, http.StatusBadRequest, "the "+side+" side is not a supported image")
			return
		}
		// The bytes may be anything under an image signature, so the answer is
		// wrapped the way the workspace file routes wrap content that is not
		// what it looks like: no sniffing, a sandboxed CSP, and no caching,
		// since the working copy can change under a cached copy.
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
		return
	}
	dto := buildChangedFile(*change, true, true)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":      "coddy.session_change",
		"sessionId":   id,
		"path":        dto.Path,
		"status":      dto.Status,
		"additions":   dto.Additions,
		"deletions":   dto.Deletions,
		"binary":      dto.Binary,
		"truncated":   dto.Truncated,
		"digest":      dto.Digest,
		"image":       dto.Image,
		"beforeImage": dto.BeforeImage,
		"afterImage":  dto.AfterImage,
		"beforeSize":  dto.BeforeSize,
		"afterSize":   dto.AfterSize,
		"patch":       dto.Patch,
		"before":      dto.Before,
		"after":       dto.After,
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

// deleteRequest is the body of a delete: the files of the change set to
// remove from the working copy. There is no "all" - deleting everything the
// change set holds is a discard plus the tracked files, and nobody asks for
// that by accident the way an empty "paths" might slip through.
type deleteRequest struct {
	// Raw, so a "paths" that is there but null or empty is told apart from
	// one that is not there at all.
	Paths json.RawMessage `json:"paths"`
}

// coddySessionChangesDelete handles POST /coddy/sessions/{id}/changes/delete.
//
// It removes the named files of the change set from the session's folder
// without restoring HEAD: a file git does not hold goes away, and a tracked
// file becomes the deletion the next read of the change set reports.
func (s *Server) coddySessionChangesDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	// Exactly one JSON object: whatever follows it would make a destructive
	// request mean something other than what its first half says.
	var req deleteRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		writeSubagentsError(w, http.StatusBadRequest, `body must be {"paths":[...]}`)
		return
	}
	var paths []string
	if len(req.Paths) > 0 {
		if err := json.Unmarshal(req.Paths, &paths); err != nil || len(paths) == 0 {
			writeSubagentsError(w, http.StatusBadRequest, `"paths" must name at least one file`)
			return
		}
	} else {
		writeSubagentsError(w, http.StatusBadRequest, `"paths" must name at least one file`)
		return
	}
	st, cwd := s.sessionWorkspace(w, r, id)
	if cwd == "" {
		return
	}
	// Removing files under an agent that is mid-edit would leave the folder in
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

	if err := gitws.Delete(cwd, paths); err != nil {
		switch {
		case errors.Is(err, gitws.ErrNotRepository):
			writeSubagentsError(w, http.StatusBadRequest, "the session's folder is not a git repository")
		case errors.Is(err, gitws.ErrNotChanged):
			// The client asked for a file its list showed and git no longer
			// reports: committed, or put back, since it last read.
			writeSubagentsError(w, http.StatusConflict, err.Error())
		default:
			s.log.Error("delete working copy changes", "session", id, "error", err)
			// A failure halfway may have removed some files already, so
			// every window reads the folder again.
			s.publishSessionChanges(id)
			writeSubagentsError(w, http.StatusInternalServerError, "delete failed")
		}
		return
	}
	// Another window showing this session reads the new state as well.
	s.publishSessionChanges(id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":    "coddy.session_changes_deleted",
		"sessionId": id,
		"at":        time.Now().UTC().Format(time.RFC3339Nano),
	})
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
