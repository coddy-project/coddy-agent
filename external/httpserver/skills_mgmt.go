//go:build http

package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

func (s *Server) registerSkillsManagementRoutes() {
	s.mux.HandleFunc("GET /coddy/skills", s.coddySkillsGet)
	s.mux.HandleFunc("POST /coddy/skills/{name}/enable", s.coddySkillsEnablePost)
	s.mux.HandleFunc("POST /coddy/skills/{name}/disable", s.coddySkillsDisablePost)
	s.mux.HandleFunc("POST /coddy/skills/sync", s.coddySkillsSyncPost)
	s.mux.HandleFunc("POST /coddy/skills/sources", s.coddySkillsSourcesPost)
	s.mux.HandleFunc("DELETE /coddy/skills/{name}", s.coddySkillsDelete)
}

type skillRowResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	FilePath    string `json:"file_path"`
	Enabled     bool   `json:"enabled"`
	Source      string `json:"source,omitempty"` // configured source string when remote-synced
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
		}
		if sk != nil {
			row.FilePath = sk.FilePath
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
	res, err := skills.Sync(r.Context(), s.activeCfg())
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
	if err := skills.RemoveRemote(s.activeCfg(), name); err != nil {
		body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": err.Error()}})
		http.Error(w, string(body), http.StatusBadRequest)
		return
	}
	s.invalidateSlashCache()
	slog.Info("remote skill removed", "name", name)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
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
