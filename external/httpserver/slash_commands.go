//go:build http

package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

type slashListCacheEntry struct {
	signature string
	sums      []skills.SkillSummary
}

func (s *Server) skillDirsSignature(cwd string) string {
	var parts []string
	home := ""
	if s.activeCfg() != nil {
		home = strings.TrimSpace(s.activeCfg().Paths.Home)
	}
	for _, d := range s.activeCfg().Skills.Dirs {
		exp := filepath.Clean(skills.ExpandConfiguredPath(d, cwd, home))
		st, err := os.Stat(exp)
		if err != nil {
			parts = append(parts, fmt.Sprintf("%s:missing", exp))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:%d:%d", exp, st.Size(), st.ModTime().UnixNano()))
	}
	return strings.Join(parts, "|")
}

func (s *Server) listSkillSummariesCached(cwdAbs string) ([]skills.SkillSummary, error) {
	cleanCWD := filepath.Clean(cwdAbs)
	sig := s.skillDirsSignature(cleanCWD) + "|cwd:" + cleanCWD

	s.slashMu.Lock()
	if ent, ok := s.slashCache[cleanCWD]; ok && ent.signature == sig {
		out := append([]skills.SkillSummary(nil), ent.sums...)
		s.slashMu.Unlock()
		return out, nil
	}
	s.slashMu.Unlock()

	loader := skills.NewLoader(s.activeCfg().Skills.Dirs)
	loaded, err := loader.LoadAll(cleanCWD, s.activeCfg().Paths.Home, s.activeCfg().Skills.ManagedDir(s.activeCfg().Paths.Home))
	if err != nil {
		return nil, err
	}
	sums := skills.ListSkills(loaded)

	s.slashMu.Lock()
	s.slashCache[cleanCWD] = slashListCacheEntry{signature: sig, sums: append([]skills.SkillSummary(nil), sums...)}
	s.slashMu.Unlock()

	return sums, nil
}

func (s *Server) resolveSlashListCWD(w http.ResponseWriter, r *http.Request) (string, bool) {
	sid := strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID"))
	if sid == "" {
		cwd, err := session.EffectiveSessionCWD("", s.defaultCWD)
		if err != nil {
			http.Error(w, `{"error":{"message":"invalid default cwd"}}`, http.StatusInternalServerError)
			return "", false
		}
		ap, err := filepath.Abs(cwd)
		if err != nil {
			http.Error(w, `{"error":{"message":"invalid cwd"}}`, http.StatusInternalServerError)
			return "", false
		}
		return ap, true
	}
	if err := session.ValidateFolderSessionID(sid); err != nil {
		http.Error(w, `{"error":{"message":"invalid X-Coddy-Session-ID"}}`, http.StatusBadRequest)
		return "", false
	}
	st := s.mgr.SessionByID(sid)
	if st == nil {
		fs := s.mgr.FileStore()
		if fs != nil && fs.HasPersistedSnapshot(sid) {
			if _, err := s.mgr.HandleSessionLoad(r.Context(), acp.SessionLoadParams{
				SessionID: sid,
				CWD:       s.defaultCWD,
			}); err != nil {
				http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
				return "", false
			}
			st = s.mgr.SessionByID(sid)
		}
	}
	if st == nil {
		http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
		return "", false
	}
	ap, err := filepath.Abs(st.GetCWD())
	if err != nil {
		http.Error(w, `{"error":{"message":"invalid session cwd"}}`, http.StatusInternalServerError)
		return "", false
	}
	return ap, true
}

func (s *Server) coddySlashCommandsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	pageStr := strings.TrimSpace(q.Get("page"))
	pageSizeStr := strings.TrimSpace(q.Get("page_size"))
	if pageStr == "" || pageSizeStr == "" {
		http.Error(w, `{"error":{"message":"page and page_size query parameters are required"}}`, http.StatusBadRequest)
		return
	}
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		http.Error(w, `{"error":{"message":"page must be a positive integer"}}`, http.StatusBadRequest)
		return
	}
	pageSize, err := strconv.Atoi(pageSizeStr)
	if err != nil || pageSize < 1 || pageSize > 200 {
		http.Error(w, `{"error":{"message":"page_size must be between 1 and 200"}}`, http.StatusBadRequest)
		return
	}

	cwdAbs, ok := s.resolveSlashListCWD(w, r)
	if !ok {
		return
	}

	sums, err := s.listSkillSummariesCached(cwdAbs)
	if err != nil {
		http.Error(w, `{"error":{"message":"failed to load skills"}}`, http.StatusInternalServerError)
		return
	}
	prefix := strings.TrimSpace(q.Get("prefix"))
	filtered := skills.FilterSummariesByPrefix(sums, prefix)
	pageItems, total, hasMore := skills.PaginateSkillSummaries(filtered, page, pageSize)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":    "coddy.slash_commands_page",
		"items":     pageItems,
		"total":     total,
		"has_more":  hasMore,
		"page":      page,
		"page_size": pageSize,
	})
}

// coddyCommandsGet lists the deterministic built-in slash commands (/compact,
// /plugin) so the composer can surface a "Commands" group alongside skills. These
// run without an LLM turn and are not part of /coddy/slash-commands (which is
// skills only). compact appears only while compaction is enabled.
func (s *Server) coddyCommandsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	cfg := s.activeCfg()
	items := skills.BuiltinCommands(cfg != nil && cfg.Compaction.IsEnabled())
	if prefix := strings.TrimSpace(r.URL.Query().Get("prefix")); prefix != "" {
		items = skills.FilterSummariesByPrefix(items, prefix)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.commands",
		"items":  items,
	})
}
