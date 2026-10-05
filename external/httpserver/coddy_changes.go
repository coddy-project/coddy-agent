//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/linediff"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/textenc"
)

// changedFileDTO is one file in the session change set. Patch, Before and After
// are pointers so an empty string still serialises: a binary file legitimately
// has an empty patch, and the caller must be able to tell that apart from a
// field it did not ask for.
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

// registerChangesRoutes adds the session change-set endpoints behind the
// changed-files card and its diff viewers.
func (s *Server) registerChangesRoutes() {
	s.mux.HandleFunc("GET /coddy/sessions/{id}/changes", s.coddySessionChangesList)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/changes/file", s.coddySessionChangeFile)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/changes/revert", s.coddySessionChangesRevert)
}

// sessionChangeDir resolves the persisted bundle directory of a session, writing
// the HTTP error itself when the session is unknown or was never persisted.
func (s *Server) sessionChangeDir(w http.ResponseWriter, r *http.Request, id string) (*session.State, string) {
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return nil, ""
	}
	sd := strings.TrimSpace(st.GetPersistedSessionDir())
	if sd == "" {
		http.Error(w, `{"error":{"message":"session not persisted"}}`, http.StatusBadRequest)
		return nil, ""
	}
	return st, sd
}

// decodeSide turns stored file bytes into text. textenc owns the encoding
// decision for the whole tree, so a Windows-1251 source reads the same here as
// it does through the file tools. Undecodable content is reported as binary.
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

// buildChangedFile renders one aggregated change into its wire form.
func buildChangedFile(change session.FileChange, withPatch, withContent bool) changedFileDTO {
	before, beforeBinary := decodeSide(change.Before)
	after, afterBinary := decodeSide(change.After)
	binary := change.Binary || beforeBinary || afterBinary

	dto := changedFileDTO{
		Path:   change.Path,
		Status: string(change.Kind),
		Binary: binary,
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

// visibleChanges drops what no review should open with: the folders a tool
// keeps its own bookkeeping in.
//
// session.AggregateSessionChanges already filters the recorded scopes; this is
// for the working-copy ones, which report whatever git happens to track. The
// rule itself lives in internal/session so both sides answer alike.
func visibleChanges(changes []session.FileChange) []session.FileChange {
	out := changes[:0]
	for _, change := range changes {
		if session.IsToolStatePath(change.Path) {
			continue
		}
		out = append(out, change)
	}
	return out
}

// changeScope selects which set of edits a viewer request describes. The card
// only ever wants the whole session; the viewer's switcher asks for the rest.
type changeScope string

const (
	scopeSession     changeScope = "session"
	scopeTurn        changeScope = "turn"
	scopeUncommitted changeScope = "uncommitted"
	// scopeAll is scopeUncommitted plus the files git does not track yet, for
	// when the question is "what is in this folder that HEAD has not" rather
	// than "what has git noticed".
	scopeAll changeScope = "all"
)

// parseChangeScope maps the `scope` query parameter. An empty value keeps the
// whole-session default, so existing callers (the card, the review window) are
// unaffected; anything unrecognised is rejected rather than silently widened.
func parseChangeScope(raw string) (changeScope, bool) {
	switch changeScope(strings.ToLower(strings.TrimSpace(raw))) {
	case "", scopeSession:
		return scopeSession, true
	case scopeTurn:
		return scopeTurn, true
	case scopeUncommitted:
		return scopeUncommitted, true
	case scopeAll:
		return scopeAll, true
	}
	return "", false
}

// changeSetResult is one scope's change set plus the two facts only the
// working-copy scopes produce.
type changeSetResult struct {
	changes []session.FileChange
	// untracked counts untracked files the scope did not show: under
	// scopeUncommitted that is every one of them, under scopeAll only those
	// past the cap. Either way it is what the viewer reports as skipped.
	untracked int
	// vcs names the system that answered ("git") and is empty when the
	// workspace is under none. The viewer says so instead of showing an empty
	// diff and leaving the reader to guess why.
	vcs string
}

// workingCopyVCS reports which version control system governs cwd. An empty
// result is a plain folder, or a git client that is not installed.
func (s *Server) workingCopyVCS(cwd string) string {
	if cwd == "" {
		return ""
	}
	if gitws.GitAvailable() && gitws.Describe(cwd).IsGitRepo {
		return "git"
	}
	return ""
}

// loadChangeSet resolves one scope into the shape every viewer renders.
func (s *Server) loadChangeSet(st *session.State, sessionDir string, scope changeScope) (changeSetResult, error) {
	// A running turn has no stored diff yet; the card opened mid-turn still has
	// to show what it wrote, so it counts as the newest turn.
	live, running := s.liveTurnDiff(st.GetID())

	switch scope {
	case scopeTurn:
		if running {
			return changeSetResult{changes: visibleChanges(session.AggregateWorkspaceDiff(live))}, nil
		}
		turn, err := session.LatestTurnNumber(sessionDir)
		if err != nil {
			return changeSetResult{}, err
		}
		changes, err := session.AggregateTurnChanges(sessionDir, turn)
		return changeSetResult{changes: visibleChanges(changes)}, err

	case scopeUncommitted, scopeAll:
		cwd := strings.TrimSpace(st.GetCWD())
		// Resolved here rather than inside the client calls so the response can
		// tell "nothing changed" apart from "this folder is not versioned".
		vcs := s.workingCopyVCS(cwd)
		if vcs == "" {
			return changeSetResult{}, nil
		}
		withUnversioned := scope == scopeAll

		var work []gitws.WorkChange
		var untracked int
		var err error
		if withUnversioned {
			work, untracked, err = gitws.WorktreeChanges(cwd)
		} else {
			work, untracked, err = gitws.UncommittedChanges(cwd)
		}
		if err != nil {
			return changeSetResult{}, err
		}
		changes := make([]session.FileChange, 0, len(work))
		for _, w := range work {
			changes = append(changes, fileChangeFromWork(w))
		}
		return changeSetResult{changes: visibleChanges(changes), untracked: untracked, vcs: vcs}, nil

	default:
		if running {
			changes, err := session.AggregateSessionChangesWithLive(sessionDir, live)
			return changeSetResult{changes: visibleChanges(changes)}, err
		}
		changes, err := session.AggregateSessionChanges(sessionDir)
		return changeSetResult{changes: visibleChanges(changes)}, err
	}
}

// loadChangeFile resolves the one file the detail route was asked for.
//
// It exists so that route does not pay for the whole change set. Under the git
// scope that difference is a git subprocess per changed file, on every request,
// and the viewer makes one request per file - which turned opening a review of
// a large working copy into tens of seconds of pointless blob reads.
func (s *Server) loadChangeFile(st *session.State, sessionDir string, scope changeScope, path string) ([]session.FileChange, error) {
	if session.IsToolStatePath(path) {
		// Hidden from the list, so not reachable one at a time either.
		return nil, nil
	}
	if scope != scopeUncommitted && scope != scopeAll {
		// The session scopes fold JSON already on disk, so narrowing afterwards
		// costs nothing worth a second code path.
		set, err := s.loadChangeSet(st, sessionDir, scope)
		return set.changes, err
	}
	cwd := strings.TrimSpace(st.GetCWD())
	withUnversioned := scope == scopeAll

	if s.workingCopyVCS(cwd) != "git" {
		return nil, nil
	}
	var work *gitws.WorkChange
	var err error
	if withUnversioned {
		work, err = gitws.WorktreeChangeFor(cwd, path)
	} else {
		work, err = gitws.UncommittedChangeFor(cwd, path)
	}
	if err != nil || work == nil {
		return nil, err
	}
	return []session.FileChange{fileChangeFromWork(*work)}, nil
}

// fileChangeFromWork adapts a git working-copy change to the session shape.
// Binary is left false on purpose: buildChangedFile detects it per side through
// textenc, which is the one place that decision is made.
func fileChangeFromWork(w gitws.WorkChange) session.FileChange {
	kind := session.FileModified
	switch w.Status {
	case "added":
		kind = session.FileAdded
	case "deleted":
		kind = session.FileDeleted
	}
	return session.FileChange{Path: w.Path, Kind: kind, Before: w.Before, After: w.After}
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
//	{ "object": "coddy.session_changes", "sessionId": "...",
//	  "files": [ { "path": "...", "status": "modified", "additions": 2, "deletions": 1,
//	               "binary": false, "truncated": false } ],
//	  "totals": { "files": 1, "additions": 2, "deletions": 1 } }
//
// `?include=patch,content` adds the unified patch and the decoded file sides.
// `?scope=session|turn|uncommitted|all` picks the change set; the working-copy
// scopes also report `untracked`, `vcsAvailable` and `vcs`.
func (s *Server) coddySessionChangesList(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	scope, ok := parseChangeScope(r.URL.Query().Get("scope"))
	if !ok {
		http.Error(w, `{"error":{"message":"unknown scope"}}`, http.StatusBadRequest)
		return
	}
	st, sd := s.sessionChangeDir(w, r, id)
	if sd == "" {
		return
	}
	set, err := s.loadChangeSet(st, sd, scope)
	if err != nil {
		s.log.Error("load session changes", "session", id, "scope", scope, "error", err)
		http.Error(w, `{"error":{"message":"read failed"}}`, http.StatusInternalServerError)
		return
	}

	withPatch, withContent := parseChangeIncludes(r.URL.Query().Get("include"))
	files := make([]changedFileDTO, 0, len(set.changes))
	totalAdd, totalDel := 0, 0
	for _, change := range set.changes {
		dto := buildChangedFile(change, withPatch, withContent)
		totalAdd += dto.Additions
		totalDel += dto.Deletions
		files = append(files, dto)
	}

	body := map[string]interface{}{
		"object":    "coddy.session_changes",
		"sessionId": id,
		"scope":     string(scope),
		"files":     files,
		"totals": map[string]int{
			"files":     len(files),
			"additions": totalAdd,
			"deletions": totalDel,
		},
	}
	// Only the git scopes can report these, and a caller must not read a missing
	// field as "no untracked files" when the question was never asked.
	if scope == scopeUncommitted || scope == scopeAll {
		body["untracked"] = set.untracked
		body["vcsAvailable"] = set.vcs != ""
		body["vcs"] = set.vcs
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// coddySessionChangeFile handles GET /coddy/sessions/{id}/changes/file?path=...
//
// The path is matched against the session's change set rather than resolved on
// disk, so this route cannot be pointed at a file the session never touched.
func (s *Server) coddySessionChangeFile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	path := r.URL.Query().Get("path")
	if strings.TrimSpace(path) == "" {
		http.Error(w, `{"error":{"message":"path is required"}}`, http.StatusBadRequest)
		return
	}
	// The same scope as the list it was opened from, or expanding a row would
	// show the session's diff while the list showed one turn's.
	scope, ok := parseChangeScope(r.URL.Query().Get("scope"))
	if !ok {
		http.Error(w, `{"error":{"message":"unknown scope"}}`, http.StatusBadRequest)
		return
	}
	st, sd := s.sessionChangeDir(w, r, id)
	if sd == "" {
		return
	}
	changes, err := s.loadChangeFile(st, sd, scope, path)
	if err != nil {
		s.log.Error("load session change", "session", id, "scope", scope, "error", err)
		http.Error(w, `{"error":{"message":"read failed"}}`, http.StatusInternalServerError)
		return
	}
	// The change set is the only thing consulted; nothing is resolved on disk,
	// so a traversing path simply names no changed file. Separators are levelled
	// because the session scope reports OS-shaped paths while a caller may well
	// send the same file with the other kind of separator: the change set of a
	// Windows session carries backslashes and a Linux one slashes, and either
	// caller must match either set, so both sides are canonicalised to slashes
	// (filepath.FromSlash alone is a no-op on Linux and leaves a backslash
	// request unmatched).
	want := changePathKey(path)
	for _, change := range changes {
		if changePathKey(change.Path) != want {
			continue
		}
		dto := buildChangedFile(change, true, true)
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
		return
	}
	http.Error(w, `{"error":{"message":"file not changed in this session"}}`, http.StatusNotFound)
}

// coddySessionChangesRevert handles POST /coddy/sessions/{id}/changes/revert.
//
// It reverses every stored turn diff of the session, which restores files the
// session edited and removes files it created. git is not involved.
func (s *Server) coddySessionChangesRevert(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	st, sd := s.sessionChangeDir(w, r, id)
	if sd == "" {
		return
	}
	// Rewriting files under an agent that is mid-edit would leave the workspace
	// in a state neither side expects.
	if s.sessionTurnActive(id) {
		writeSubagentsError(w, http.StatusConflict, "session "+id+" has a turn in flight")
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
	cwd := strings.TrimSpace(st.GetCWD())
	if cwd == "" {
		http.Error(w, `{"error":{"message":"session has no workspace"}}`, http.StatusBadRequest)
		return
	}
	note, err := session.RestoreWorkspaceFiles(cwd, sd, 0)
	if err != nil {
		s.log.Error("revert session changes", "session", id, "error", err)
		status := http.StatusInternalServerError
		if errors.Is(err, session.ErrWorkspaceConflict) {
			status = http.StatusConflict
		}
		writeSubagentsError(w, status, err.Error())
		return
	}
	// The workspace is back where the session found it, so the recorded diffs
	// no longer describe anything true; leaving them would keep the card
	// reporting work that has been undone.
	if err := session.ClearStoredTurnDiffs(sd); err != nil {
		s.log.Warn("clear turn diffs after revert", "session", id, "error", err)
		writeSubagentsError(w, http.StatusInternalServerError, "could not clear turn diffs after rollback")
		return
	}
	// Another window showing this session reads the empty set as well.
	s.publishSessionChanges(id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":    "coddy.session_changes_reverted",
		"sessionId": id,
		"note":      note,
	})
}

// changePathKey canonicalises a workspace path for change-set matching: to
// slash form on every platform, so a request and a recorded change agree
// whichever separator each of them was shaped with.
func changePathKey(p string) string {
	return strings.ReplaceAll(filepath.ToSlash(p), `\`, "/")
}
