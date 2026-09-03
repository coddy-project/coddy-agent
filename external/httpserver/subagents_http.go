//go:build http

package httpserver

// Subagent surface of the REST API: the definition catalog of a workspace with
// the trust state of every entry, the approval routes that write and remove
// project-scope receipts, and the helpers every session route uses to keep a
// child session's transcript read-only.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
)

// registerSubagentRoutes wires the catalog and the approval routes next to the
// other coddy routes.
func (s *Server) registerSubagentRoutes() {
	s.mux.HandleFunc("GET /coddy/subagents", s.coddySubagentsList)
	s.mux.HandleFunc("POST /coddy/subagents/{name}/trust", s.coddySubagentTrust)
	s.mux.HandleFunc("POST /coddy/subagents/{name}/untrust", s.coddySubagentUntrust)
}

// subagentWorkspaceCWD resolves the workspace a catalog request refers to: the
// caller's cwd when given, else the server's default cwd. A relative path is
// refused because trust receipts are keyed by canonical absolute workspaces and
// a path relative to the server process would approve the wrong checkout.
func (s *Server) subagentWorkspaceCWD(raw string) (string, error) {
	cwd := strings.TrimSpace(raw)
	if cwd == "" {
		cwd = s.defaultCWD
	}
	if !filepath.IsAbs(cwd) {
		return "", fmt.Errorf("cwd must be an absolute path")
	}
	return filepath.Clean(cwd), nil
}

// subagentCatalog is the resolved catalog of one workspace under the live
// configuration: definitions, policy, canonical workspace key and the store the
// trust decisions are read from.
type subagentCatalog struct {
	workspace string
	policy    string
	defs      []*subagents.Definition
	store     *subagents.TrustStore
}

// loadSubagentCatalog loads the definitions visible from cwd the way the agent
// runtime does for a session with that cwd, so the catalog and spawn_agent agree.
func (s *Server) loadSubagentCatalog(cwd string) subagentCatalog {
	cfg := s.activeCfg()
	policy := cfg.Subagents.ResolvedProjectTrust()
	loader := subagents.NewLoader(cfg.Subagents.Dirs, policy)
	loader.Log = s.log
	return subagentCatalog{
		workspace: subagents.CanonicalWorkspace(cwd),
		policy:    policy,
		defs:      loader.Load(cwd, cfg.Paths.Home),
		store:     subagents.NewTrustStore(cfg.Paths.Home),
	}
}

func (c subagentCatalog) entries() []subagents.CatalogEntry {
	return subagents.BuildCatalog(c.defs, c.policy, c.workspace, c.store)
}

// entry returns the catalog row of one definition with its trust decision
// taken now, so a response after an approval already shows the new state.
func (c subagentCatalog) entry(def *subagents.Definition) subagents.CatalogEntry {
	rows := subagents.BuildCatalog([]*subagents.Definition{def}, c.policy, c.workspace, c.store)
	return rows[0]
}

func writeSubagentsError(w http.ResponseWriter, code int, msg string) {
	http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, msg), code)
}

// coddySubagentsList answers GET /coddy/subagents: every definition visible
// from the workspace (built-ins, user scope, project scope under the current
// policy), sorted by name, with its trust state for that workspace.
func (s *Server) coddySubagentsList(w http.ResponseWriter, r *http.Request) {
	cwd, err := s.subagentWorkspaceCWD(r.URL.Query().Get("cwd"))
	if err != nil {
		writeSubagentsError(w, http.StatusBadRequest, err.Error())
		return
	}
	cat := s.loadSubagentCatalog(cwd)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":    "coddy.subagent_list",
		"workspace": cat.workspace,
		"policy":    cat.policy,
		"items":     cat.entries(),
	})
}

// subagentTrustRequest is the optional JSON body of the trust routes. An empty
// body means the server's default workspace.
type subagentTrustRequest struct {
	CWD string `json:"cwd"`
}

// resolveSubagentTrustTarget parses the body, resolves the workspace and finds
// the named definition. It writes the error response itself and reports false
// when the caller must stop.
func (s *Server) resolveSubagentTrustTarget(w http.ResponseWriter, r *http.Request) (subagentCatalog, *subagents.Definition, bool) {
	var body subagentTrustRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeSubagentsError(w, http.StatusBadRequest, "invalid JSON")
		return subagentCatalog{}, nil, false
	}
	cwd, err := s.subagentWorkspaceCWD(body.CWD)
	if err != nil {
		writeSubagentsError(w, http.StatusBadRequest, err.Error())
		return subagentCatalog{}, nil, false
	}
	name := strings.TrimSpace(r.PathValue("name"))
	cat := s.loadSubagentCatalog(cwd)
	def := subagents.FindByName(cat.defs, name)
	if def == nil {
		writeSubagentsError(w, http.StatusNotFound, fmt.Sprintf("subagent %q not found for workspace %s", name, cat.workspace))
		return subagentCatalog{}, nil, false
	}
	return cat, def, true
}

// coddySubagentTrust records a receipt for the current content of a
// project-scope definition in the given workspace. Built-ins and user-scope
// files need no approval, so asking for one is a client error rather than a
// silent no-op.
func (s *Server) coddySubagentTrust(w http.ResponseWriter, r *http.Request) {
	cat, def, ok := s.resolveSubagentTrustTarget(w, r)
	if !ok {
		return
	}
	switch {
	case def.Builtin:
		writeSubagentsError(w, http.StatusBadRequest, fmt.Sprintf("subagent %q is built in and needs no approval", def.Name))
		return
	case def.Scope != subagents.ScopeProject:
		writeSubagentsError(w, http.StatusBadRequest, fmt.Sprintf("subagent %q is a %s-scope definition (%s) and needs no approval", def.Name, def.Scope, def.Path))
		return
	}
	if err := cat.store.Approve(cat.workspace, def); err != nil {
		s.log.Error("subagent trust", "name", def.Name, "workspace", cat.workspace, "error", err)
		writeSubagentsError(w, http.StatusInternalServerError, "could not record the approval")
		return
	}
	s.log.Info("subagent approved for workspace", "name", def.Name, "workspace", cat.workspace, "digest", def.Digest)
	writeSubagentEntry(w, cat.entry(def))
}

// coddySubagentUntrust removes the receipt of a definition in the given
// workspace. Withdrawing an approval that was never on file changes nothing and
// still answers with the current entry.
func (s *Server) coddySubagentUntrust(w http.ResponseWriter, r *http.Request) {
	cat, def, ok := s.resolveSubagentTrustTarget(w, r)
	if !ok {
		return
	}
	removed, err := cat.store.Revoke(cat.workspace, def.Name)
	if err != nil {
		s.log.Error("subagent untrust", "name", def.Name, "workspace", cat.workspace, "error", err)
		writeSubagentsError(w, http.StatusInternalServerError, "could not remove the approval")
		return
	}
	s.log.Info("subagent approval revoked", "name", def.Name, "workspace", cat.workspace, "removed", removed)
	writeSubagentEntry(w, cat.entry(def))
}

func writeSubagentEntry(w http.ResponseWriter, entry subagents.CatalogEntry) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.subagent",
		"item":   entry,
	})
}

// subagentLink is the JSON object a child session's rows and transcript carry,
// so a client can route from the transcript back to the parent chat and to the
// task that represents the run in the parent's tasks drawer.
func subagentLink(parentSessionID, name, taskID string) map[string]interface{} {
	return map[string]interface{}{
		"parentSessionId": parentSessionID,
		"name":            name,
		"taskId":          taskID,
	}
}

// subagentSessionPrefix is the folder prefix session.NewSubagentSessionID
// gives child sessions; the listing uses it to open only child bundles.
const subagentSessionPrefix = "sub_"

// subagentRowLink reads the parent link of a child session row from its
// bundle. Only sub_ bundles are opened, so the default listing pays nothing.
func subagentRowLink(fs *session.FileStore, id string) map[string]interface{} {
	if !strings.HasPrefix(id, subagentSessionPrefix) {
		return nil
	}
	snap, err := fs.ReadSnapshot(id)
	if err != nil || !snap.Meta.IsSubagentRun(id) {
		return nil
	}
	return subagentLink(snap.Meta.ParentSessionID, snap.Meta.SubagentName, snap.Meta.SubagentTaskID)
}

// subagentReadOnlyMessage names the parent a caller should prompt instead.
func subagentReadOnlyMessage(st *session.State) string {
	if meta := st.Subagent(); meta != nil && strings.TrimSpace(meta.ParentSessionID) != "" {
		return fmt.Sprintf("subagent sessions are read-only transcripts; prompt the parent session %s instead", meta.ParentSessionID)
	}
	return session.ErrSubagentReadOnly.Error()
}

// rejectSubagentTurn answers 409 and reports true when st is a child session:
// resuming or messaging a subagent is not supported, so every route that would
// start a turn on it stops here. It is a no-op for ordinary sessions.
func rejectSubagentTurn(w http.ResponseWriter, st *session.State) bool {
	if st == nil || !st.IsSubagentRun() {
		return false
	}
	writeSubagentsError(w, http.StatusConflict, subagentReadOnlyMessage(st))
	return true
}

// isSubagentReadOnly maps the manager's refusal onto the HTTP 409, for paths
// where the state is not at hand when the error surfaces.
func isSubagentReadOnly(err error) bool {
	return errors.Is(err, session.ErrSubagentReadOnly)
}
