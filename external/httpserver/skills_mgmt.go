//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

func (s *Server) registerSkillsManagementRoutes() {
	s.mux.HandleFunc("GET /coddy/skills", s.coddySkillsGet)
	s.mux.HandleFunc("GET /coddy/skills/updates", s.coddySkillsUpdatesGet)
	s.mux.HandleFunc("GET /coddy/skills/available", s.coddySkillsAvailableGet)
	s.mux.HandleFunc("GET /coddy/skills/sources", s.coddySkillsSourcesGet)
	s.mux.HandleFunc("POST /coddy/skills/install", s.coddySkillsInstallPost)
	s.mux.HandleFunc("POST /coddy/skills/{name}/enable", s.coddySkillsEnablePost)
	s.mux.HandleFunc("POST /coddy/skills/{name}/disable", s.coddySkillsDisablePost)
	s.mux.HandleFunc("POST /coddy/skills/{name}/update", s.coddySkillsUpdatePost)
	s.mux.HandleFunc("POST /coddy/skills/sync", s.coddySkillsSyncPost)
	s.mux.HandleFunc("POST /coddy/skills/sources", s.coddySkillsSourcesPost)
	s.mux.HandleFunc("DELETE /coddy/skills/sources", s.coddySkillsSourcesDelete)
	s.mux.HandleFunc("POST /coddy/skills/sources/trust", s.coddySkillsSourceTrust)
	s.mux.HandleFunc("POST /coddy/skills/sources/untrust", s.coddySkillsSourceUntrust)
	s.mux.HandleFunc("DELETE /coddy/skills/{name}", s.coddySkillsDelete)
}

type skillRowResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	FilePath    string `json:"file_path"`
	Enabled     bool   `json:"enabled"`
	Version     string `json:"version,omitempty"` // installed version, when known
	Source      string `json:"source,omitempty"`  // configured source string when remote-synced
	Readonly    bool   `json:"readonly"`          // bundled skills cannot be deleted
}

// coddySkillsGet lists all skills with their enabled/disabled state. ${CWD} in
// skills.dirs resolves against the session named by the optional
// X-Coddy-Session-ID header, else the folder in the cwd query (Settings → Skills
// of a new chat), else the server default workspace, the same way
// /coddy/slash-commands does.
func (s *Server) coddySkillsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	cfg := s.activeCfg()
	installDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	loader := skills.NewLoader(cfg.Skills.SearchDirs())

	cwd, ok := s.resolveListingCWD(w, r)
	if !ok {
		return
	}
	allLoaded, err := loader.LoadAll(cwd, cfg.Paths.Home)
	if err != nil {
		http.Error(w, `{"error":{"message":"failed to load skills"}}`, http.StatusInternalServerError)
		return
	}
	disabled := skills.ReadDisabled(installDir)
	remote := skills.RemoteSources(cfg)
	sums := skills.ListSkills(allLoaded)

	byName := make(map[string]*skills.Skill, len(allLoaded))
	for _, sk := range allLoaded {
		n := skills.CanonicalCommandName(sk)
		if _, ok := byName[n]; !ok {
			byName[n] = sk
		}
	}

	rows := make([]skillRowResponse, 0, len(sums))
	for _, sum := range sums {
		sk := byName[sum.Name]
		row := skillRowResponse{
			Name:        sum.Name,
			Description: sum.Description,
			Enabled:     !skills.IsDisabled(disabled, sum.Name),
			Version:     skills.InstalledVersion(remote, sum.Name, sk),
		}
		if sk != nil {
			row.FilePath = sk.FilePath
			row.Readonly = skills.SkillReadonly(sk)
		}
		if ent, ok := remote[sum.Name]; ok {
			row.Source = ent.Source
		}
		rows = append(rows, row)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.skills_list",
		"items":  rows,
	})
}

// coddySkillsEnablePost removes a skill from the disabled list.
func (s *Server) coddySkillsEnablePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	name := r.PathValue("name")
	if err := skills.Enable(s.activeCfg(), name); err != nil {
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), http.StatusBadRequest)
		return
	}
	slog.Info("skill enabled", "name", name)
	s.slashMu.Lock()
	s.slashCache = make(map[string]slashListCacheEntry)
	s.slashMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// coddySkillsDisablePost adds a skill to the disabled list.
func (s *Server) coddySkillsDisablePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	name := r.PathValue("name")
	if err := skills.Disable(s.activeCfg(), name); err != nil {
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), http.StatusBadRequest)
		return
	}
	slog.Info("skill disabled", "name", name)
	s.slashMu.Lock()
	s.slashCache = make(map[string]slashListCacheEntry)
	s.slashMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// coddySkillsSyncPost fetches the skill sources in effect for the session
// workspace (the server default workspace without a session) and
// materializes them; a project entry the trust gate holds back is reported in
// held, not fetched.
func (s *Server) coddySkillsSyncPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	cwd, ok := s.resolveSessionCWD(w, r)
	if !ok {
		return
	}
	// Optional ?source=<src>: only that source, or only what is installed from
	// it when it is a marketplace; otherwise sync all.
	var res *skills.SyncResult
	var err error
	if src := strings.TrimSpace(r.URL.Query().Get("source")); src != "" {
		res, _, err = skills.UpdateSource(r.Context(), s.activeCfg(), cwd, src)
	} else {
		res, err = skills.Sync(r.Context(), s.activeCfg(), cwd)
	}
	if err != nil {
		// An entry the trust gate holds back is the caller's to approve, not
		// a failure of the server.
		status := http.StatusInternalServerError
		if errors.Is(err, skills.ErrHeld) {
			status = http.StatusBadRequest
		}
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), status)
		return
	}
	s.invalidateSlashCache()
	slog.Info("skills synced", "added", len(res.Added), "updated", len(res.Updated), "failed", len(res.Failed))
	w.Header().Set("Content-Type", "application/json")
	held := res.Held
	if held == nil {
		held = []skills.Declaration{}
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"added":   res.Added,
		"updated": res.Updated,
		"failed":  res.Failed,
		"held":    held,
	})
}

type skillSourceRequest struct {
	Source string `json:"source"`
	Sync   bool   `json:"sync"`
	// Scope picks the file: "global" (default) <home>/marketplaces.json,
	// "local" the session workspace's .coddy/marketplaces.json.
	Scope string `json:"scope,omitempty"`
	// Kind is "source" (default, installed whole) or "marketplace" (a
	// catalog whose plugins are installed one by one).
	Kind string `json:"kind,omitempty"`
}

// coddySkillsSourcesPost declares a remote source (or a marketplace) in the
// marketplaces.json the scope names, and optionally syncs it. A local entry
// is approved for the workspace by the act of writing it.
func (s *Server) coddySkillsSourcesPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	cwd, ok := s.resolveSessionCWD(w, r)
	if !ok {
		return
	}
	var req skillSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":{"message":"invalid request body"}}`, http.StatusBadRequest)
		return
	}
	cfg := s.activeCfg()
	var added bool
	var err error
	switch strings.TrimSpace(req.Kind) {
	case "", skills.KindSource:
		added, err = skills.AddSource(cfg, cwd, req.Source, req.Scope)
	case skills.KindMarketplace:
		if req.Sync {
			// Declaring a marketplace installs nothing; syncing its source
			// would install every plugin it lists and record them so.
			http.Error(w, `{"error":{"message":"a marketplace installs nothing on its own: install its plugins one by one (POST /coddy/skills/install, or plugin install <plugin>@<marketplace>), or add it with kind source to have every plugin installed"}}`, http.StatusBadRequest)
			return
		}
		var refreshed bool
		_, refreshed, err = skills.AddMarketplace(r.Context(), cfg, cwd, req.Source, req.Scope)
		added = !refreshed
	default:
		err = fmt.Errorf("unknown kind %q (use %q or %q)", req.Kind, skills.KindSource, skills.KindMarketplace)
	}
	if err != nil {
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), http.StatusBadRequest)
		return
	}

	resp := map[string]interface{}{"ok": true, "added": added}
	if req.Sync {
		// The source just added, as documented; the others are refreshed by
		// POST /coddy/skills/sync.
		res, err := skills.SyncSource(r.Context(), s.activeCfg(), req.Source)
		if err != nil {
			body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
			http.Error(w, string(body), http.StatusInternalServerError)
			return
		}
		s.invalidateSlashCache()
		resp["sync"] = map[string]interface{}{"added": res.Added, "updated": res.Updated, "failed": res.Failed}
	}
	slog.Info("skill source added", "source", req.Source, "added", added)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// coddySkillsDelete removes a remote (synced) skill by name.
func (s *Server) coddySkillsDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.NotFound(w, r)
		return
	}
	name := r.PathValue("name")
	// The skill is looked up in the workspace of the list it was picked from:
	// a project skill of the chat's folder is not in the server's default cwd.
	// The reach is the listing's (a session, else cwd, else the default) and
	// DeleteSkill still removes only a path inside a configured skills
	// directory of that workspace; for a skill linked in from elsewhere that
	// path is the link, so what it points at stays on disk.
	cwd, ok := s.resolveListingCWD(w, r)
	if !ok {
		return
	}
	if err := skills.DeleteSkill(s.activeCfg(), cwd, name); err != nil {
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), http.StatusBadRequest)
		return
	}
	s.invalidateSlashCache()
	slog.Info("skill deleted", "name", name)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// coddySkillsUpdatesGet reports, per installed remote skill, whether a newer
// version is available in its marketplace source (performs network/git
// access). A source a project of the workspace declares and the trust gate
// holds back is not contacted.
func (s *Server) coddySkillsUpdatesGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	cwd, ok := s.resolveListingCWD(w, r)
	if !ok {
		return
	}
	statuses, err := skills.CheckUpdates(r.Context(), s.activeCfg(), cwd)
	if err != nil {
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.skills_updates",
		"items":  statuses,
	})
}

// coddySkillsAvailableGet lists installable plugins advertised by the configured
// marketplaces (network / git access), each flagged with whether it is already
// installed. Backs the browse/filter install control.
func (s *Server) coddySkillsAvailableGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	cwd, ok := s.resolveListingCWD(w, r)
	if !ok {
		return
	}
	items, err := skills.AvailablePlugins(r.Context(), s.activeCfg(), cwd)
	if err != nil {
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.skills_available",
		"items":  items,
	})
}

type skillInstallRequest struct {
	Source string `json:"source"`
	Plugin string `json:"plugin"`
}

// coddySkillsInstallPost installs a single plugin from a marketplace source.
func (s *Server) coddySkillsInstallPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req skillInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":{"message":"invalid request body"}}`, http.StatusBadRequest)
		return
	}
	res, err := skills.InstallPlugin(r.Context(), s.activeCfg(), req.Source, req.Plugin)
	if err == nil && len(res.Added)+len(res.Updated) == 0 && len(res.Failed) > 0 {
		// Nothing installed: an archive refused, a plugin without skills. The
		// client reports the reason instead of a success.
		err = errors.New("install " + req.Plugin + ": " + res.Failed[0].Error)
	}
	if err != nil {
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), http.StatusBadRequest)
		return
	}
	s.invalidateSlashCache()
	slog.Info("plugin installed", "source", req.Source, "plugin", req.Plugin, "added", len(res.Added), "updated", len(res.Updated))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"added":   res.Added,
		"updated": res.Updated,
		"failed":  res.Failed,
	})
}

// coddySkillsSourcesGet lists the skill sources and marketplaces declared
// for the session workspace (the server default workspace without a session,
// as the MCP tab does): entries carries each with its kind, origin, file and
// trust state; items are the sources installed whole in effect and system
// the subset Coddy brings itself, which DELETE refuses.
func (s *Server) coddySkillsSourcesGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	cwd, ok := s.resolveSessionCWD(w, r)
	if !ok {
		return
	}
	cfg := s.activeCfg()
	decls, errs := skills.Declarations(cfg, cwd)
	if decls == nil {
		decls = []skills.Declaration{}
	}
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	system := make([]string, 0, len(skills.SystemSources))
	system = append(system, skills.SystemSources...)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":        "coddy.skills_sources",
		"workspace":     cwd,
		"project_trust": cfg.Skills.ResolvedProjectTrust(),
		"items":         skills.ListSources(cfg, cwd),
		"system":        system,
		"entries":       decls,
		"errors":        messages,
	})
}

type skillSourceTrustRequest struct {
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// coddySkillsSourceTrust approves a project entry of the session workspace's
// .coddy/marketplaces.json. The optional fingerprint names the entry the
// operator was shown: one the checkout rewrote since is refused with 409.
func (s *Server) coddySkillsSourceTrust(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.resolveSessionCWD(w, r)
	if !ok {
		return
	}
	var req skillSourceTrustRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Key) == "" {
		http.Error(w, `{"error":{"message":"body must name the entry: {\"key\": \"<marketplace | source>\"}"}}`, http.StatusBadRequest)
		return
	}
	d, err := skills.ApproveShown(s.activeCfg(), cwd, req.Key, req.Fingerprint)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, skills.ErrDeclarationChanged) {
			status = http.StatusConflict
		}
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), status)
		return
	}
	slog.Info("project skill source approved", "key", req.Key, "workspace", cwd)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "fingerprint": d.Fingerprint})
}

// coddySkillsSourceUntrust withdraws the approval of a project entry.
func (s *Server) coddySkillsSourceUntrust(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.resolveSessionCWD(w, r)
	if !ok {
		return
	}
	var req skillSourceTrustRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Key) == "" {
		http.Error(w, `{"error":{"message":"body must name the entry: {\"key\": \"<marketplace | source>\"}"}}`, http.StatusBadRequest)
		return
	}
	removed, err := skills.Revoke(s.activeCfg(), cwd, req.Key)
	if err != nil {
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "removed": removed})
}

// coddySkillsUpdatePost re-syncs the source that provides {name}, installing the
// version that source currently declares.
func (s *Server) coddySkillsUpdatePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	cwd, ok := s.resolveSessionCWD(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	res, err := skills.UpdateSkill(r.Context(), s.activeCfg(), cwd, name)
	if err != nil {
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), http.StatusBadRequest)
		return
	}
	s.invalidateSlashCache()
	slog.Info("skill updated", "name", name, "added", len(res.Added), "updated", len(res.Updated), "failed", len(res.Failed))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"added":   res.Added,
		"updated": res.Updated,
		"failed":  res.Failed,
	})
}

// coddySkillsSourcesDelete takes the sources and marketplaces ?source= names
// (a marketplace by name, either kind by source) out of the operator's
// marketplaces.json and the session workspace's, or only out of the one
// ?origin= names.
func (s *Server) coddySkillsSourcesDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.NotFound(w, r)
		return
	}
	cwd, ok := s.resolveSessionCWD(w, r)
	if !ok {
		return
	}
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	if source == "" {
		http.Error(w, `{"error":{"message":"missing source query parameter"}}`, http.StatusBadRequest)
		return
	}
	// origin (home or project) limits the removal to the file of the row a
	// client showed; without it both files are edited.
	removed, err := skills.RemoveSource(s.activeCfg(), cwd, source, r.URL.Query().Get("origin"))
	if err != nil {
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), http.StatusBadRequest)
		return
	}
	slog.Info("skill source removed", "source", source, "removed", removed)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "removed": removed})
}

func (s *Server) invalidateSlashCache() {
	s.slashMu.Lock()
	s.slashCache = make(map[string]slashListCacheEntry)
	s.slashMu.Unlock()
}

// reloadConfigFromDisk re-reads config.yaml (after a route persisted it) and
// swaps it into the running server and session manager.
func (s *Server) reloadConfigFromDisk() {
	c := s.activeCfg()
	if c == nil {
		return
	}
	reloaded, err := config.LoadWithPaths(c.Paths)
	if err != nil {
		s.log.Error("skills config reload", "error", err)
		return
	}
	s.ReplaceConfig(reloaded)
	s.mgr.ReplaceConfig(reloaded)
}
