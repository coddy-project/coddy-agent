//go:build http

package httpserver

import (
	"encoding/json"
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

// coddySkillsGet lists all skills with their enabled/disabled state.
func (s *Server) coddySkillsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	cfg := s.activeCfg()
	installDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	loader := skills.NewLoader(cfg.Skills.Dirs)

	allLoaded, err := loader.LoadAll(s.defaultCWD, cfg.Paths.Home)
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

// coddySkillsSyncPost fetches all configured skill sources and materializes them.
func (s *Server) coddySkillsSyncPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	// Optional ?source=<src>: sync only that marketplace; otherwise sync all.
	var res *skills.SyncResult
	var err error
	if src := strings.TrimSpace(r.URL.Query().Get("source")); src != "" {
		res, err = skills.SyncSource(r.Context(), s.activeCfg(), src)
	} else {
		res, err = skills.Sync(r.Context(), s.activeCfg())
	}
	if err != nil {
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), http.StatusInternalServerError)
		return
	}
	s.invalidateSlashCache()
	slog.Info("skills synced", "added", len(res.Added), "updated", len(res.Updated), "failed", len(res.Failed))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"added":   res.Added,
		"updated": res.Updated,
		"failed":  res.Failed,
	})
}

type skillSourceRequest struct {
	Source string `json:"source"`
	Sync   bool   `json:"sync"`
}

// coddySkillsSourcesPost adds a remote source to skills.sources (and optionally syncs).
func (s *Server) coddySkillsSourcesPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req skillSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":{"message":"invalid request body"}}`, http.StatusBadRequest)
		return
	}
	cfg := s.activeCfg()
	added, err := skills.AddSource(cfg, req.Source)
	if err != nil {
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), http.StatusBadRequest)
		return
	}
	// AddSource persisted config.yaml; reload so the running server sees it.
	s.reloadConfigFromDisk()

	resp := map[string]interface{}{"ok": true, "added": added}
	if req.Sync {
		res, err := skills.Sync(r.Context(), s.activeCfg())
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
	if err := skills.DeleteSkill(s.activeCfg(), s.defaultCWD, name); err != nil {
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
// version is available in its marketplace source (performs network/git access).
func (s *Server) coddySkillsUpdatesGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	statuses, err := skills.CheckUpdates(r.Context(), s.activeCfg())
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
	items, err := skills.AvailablePlugins(r.Context(), s.activeCfg(), s.defaultCWD)
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

// coddySkillsSourcesGet lists configured remote skill sources.
func (s *Server) coddySkillsSourcesGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.skills_sources",
		"items":  skills.ListSources(s.activeCfg()),
	})
}

// coddySkillsUpdatePost re-syncs the source that provides {name}, installing the
// version that source currently declares.
func (s *Server) coddySkillsUpdatePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	name := r.PathValue("name")
	res, err := skills.UpdateSkill(r.Context(), s.activeCfg(), name)
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

// coddySkillsSourcesDelete removes a source from skills.sources (query ?source=).
func (s *Server) coddySkillsSourcesDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.NotFound(w, r)
		return
	}
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	if source == "" {
		http.Error(w, `{"error":{"message":"missing source query parameter"}}`, http.StatusBadRequest)
		return
	}
	removed, err := skills.RemoveSource(s.activeCfg(), source)
	if err != nil {
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), http.StatusBadRequest)
		return
	}
	s.reloadConfigFromDisk()
	slog.Info("skill source removed", "source", source, "removed", removed)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "removed": removed})
}

func (s *Server) invalidateSlashCache() {
	s.slashMu.Lock()
	s.slashCache = make(map[string]slashListCacheEntry)
	s.slashMu.Unlock()
}

// reloadConfigFromDisk re-reads config.yaml (after AddSource persisted it) and
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
