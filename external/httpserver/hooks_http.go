//go:build http

package httpserver

// Hooks surface of the REST API: the catalog of hook definition files a
// workspace would load, with the trust state of every file, and the approval
// routes that write and remove project-scope receipts.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
)

// registerHookRoutes wires the catalog and the approval routes next to the
// other coddy routes.
func (s *Server) registerHookRoutes() {
	s.mux.HandleFunc("GET /coddy/hooks", s.coddyHooksList)
	s.mux.HandleFunc("POST /coddy/hooks/trust", s.coddyHooksTrust)
	s.mux.HandleFunc("POST /coddy/hooks/untrust", s.coddyHooksUntrust)
}

// hookCatalog is the resolved catalog of one workspace under the live
// configuration: sources, policy, canonical workspace key and the store the
// trust decisions are read from.
type hookCatalog struct {
	workspace string
	policy    string
	sources   []*hooks.Source
	store     *hooks.TrustStore
}

// loadHookCatalog loads the files visible from cwd the way the agent runtime
// does for a session with that cwd, so the catalog and the loop agree.
func (s *Server) loadHookCatalog(cwd string) hookCatalog {
	cfg := s.activeCfg()
	policy := cfg.Hooks.ResolvedProjectTrust()
	store := hooks.NewTrustStore(cfg.Paths.Home)
	loader := hooks.NewLoader(cfg.Hooks.Files, policy).WithStore(store)
	loader.Log = s.log
	return hookCatalog{
		workspace: hooks.CanonicalWorkspace(cwd),
		policy:    policy,
		sources:   loader.Load(cwd, cfg.Paths.Home),
		store:     store,
	}
}

// entry returns the catalog row of one source with its trust decision taken
// now, so a response after an approval already shows the new state.
func (c hookCatalog) entry(src *hooks.Source, cwd, home string, cfgFiles []string) hooks.CatalogEntry {
	reloaded := hooks.NewLoader(cfgFiles, c.policy).WithStore(c.store).Load(cwd, home)
	if fresh := hooks.FindSource(reloaded, src.Display); fresh != nil {
		return hooks.BuildCatalog([]*hooks.Source{fresh})[0]
	}
	return hooks.BuildCatalog([]*hooks.Source{src})[0]
}

func writeHooksError(w http.ResponseWriter, code int, msg string) {
	http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, msg), code)
}

// coddyHooksList answers GET /coddy/hooks: every definition file visible from
// the workspace (user scope, project scope under the current policy), in load
// order, with its hooks and its trust state for that workspace.
func (s *Server) coddyHooksList(w http.ResponseWriter, r *http.Request) {
	cwd, err := s.subagentWorkspaceCWD(r.URL.Query().Get("cwd"))
	if err != nil {
		writeHooksError(w, http.StatusBadRequest, err.Error())
		return
	}
	cat := s.loadHookCatalog(cwd)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":    "coddy.hook_list",
		"workspace": cat.workspace,
		"policy":    cat.policy,
		"items":     hooks.BuildCatalog(cat.sources),
	})
}

// hookTrustRequest is the JSON body of the trust routes: the workspace (the
// server's default cwd when empty) and the file as the catalog names it.
type hookTrustRequest struct {
	CWD  string `json:"cwd"`
	File string `json:"file"`
}

// resolveHookTrustTarget parses the body, resolves the workspace and finds
// the named file. It writes the error response itself and reports false when
// the caller must stop.
func (s *Server) resolveHookTrustTarget(w http.ResponseWriter, r *http.Request) (hookCatalog, *hooks.Source, string, bool) {
	var body hookTrustRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeHooksError(w, http.StatusBadRequest, "invalid JSON")
		return hookCatalog{}, nil, "", false
	}
	cwd, err := s.subagentWorkspaceCWD(body.CWD)
	if err != nil {
		writeHooksError(w, http.StatusBadRequest, err.Error())
		return hookCatalog{}, nil, "", false
	}
	file := strings.TrimSpace(body.File)
	if file == "" {
		writeHooksError(w, http.StatusBadRequest, "file is required")
		return hookCatalog{}, nil, "", false
	}
	cat := s.loadHookCatalog(cwd)
	src := hooks.FindSource(cat.sources, file)
	if src == nil {
		writeHooksError(w, http.StatusNotFound, fmt.Sprintf("hooks file %q not found for workspace %s", file, cat.workspace))
		return hookCatalog{}, nil, "", false
	}
	return cat, src, cwd, true
}

// coddyHooksTrust records a receipt for the current content of a
// project-scope file in the given workspace. A user-scope file needs no
// approval and an invalid file cannot be approved, so both are client errors
// rather than silent no-ops.
func (s *Server) coddyHooksTrust(w http.ResponseWriter, r *http.Request) {
	cat, src, cwd, ok := s.resolveHookTrustTarget(w, r)
	if !ok {
		return
	}
	switch {
	case src.Scope != hooks.ScopeProject:
		writeHooksError(w, http.StatusBadRequest, fmt.Sprintf("hooks file %s is %s scope and needs no approval", src.Display, src.Scope))
		return
	case src.Err != nil:
		writeHooksError(w, http.StatusBadRequest, fmt.Sprintf("hooks file %s cannot be approved: %v", src.Display, src.Err))
		return
	}
	if err := cat.store.Approve(cat.workspace, src); err != nil {
		s.log.Error("hooks trust", "file", src.Display, "workspace", cat.workspace, "error", err)
		writeHooksError(w, http.StatusInternalServerError, "could not record the approval")
		return
	}
	s.log.Info("hooks file approved for workspace", "file", src.Display, "workspace", cat.workspace, "digest", src.Digest)
	cfg := s.activeCfg()
	writeHookEntry(w, cat.entry(src, cwd, cfg.Paths.Home, cfg.Hooks.Files))
}

// coddyHooksUntrust removes the receipt of a file in the given workspace.
// Withdrawing an approval that was never on file changes nothing and still
// answers with the current entry.
func (s *Server) coddyHooksUntrust(w http.ResponseWriter, r *http.Request) {
	cat, src, cwd, ok := s.resolveHookTrustTarget(w, r)
	if !ok {
		return
	}
	removed, err := cat.store.Revoke(cat.workspace, src.Display)
	if err != nil {
		s.log.Error("hooks untrust", "file", src.Display, "workspace", cat.workspace, "error", err)
		writeHooksError(w, http.StatusInternalServerError, "could not remove the approval")
		return
	}
	s.log.Info("hooks file approval revoked", "file", src.Display, "workspace", cat.workspace, "removed", removed)
	cfg := s.activeCfg()
	writeHookEntry(w, cat.entry(src, cwd, cfg.Paths.Home, cfg.Hooks.Files))
}

func writeHookEntry(w http.ResponseWriter, entry hooks.CatalogEntry) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.hook_source",
		"item":   entry,
	})
}
