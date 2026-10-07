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

// slashListCacheEntry caches one skill catalog per resolved workspace cwd
// (not per session): two sessions rooted in the same folder share it, and the
// signature over the expanded skill directories invalidates it on change.
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
	for _, d := range s.activeCfg().Skills.SearchDirs() {
		raw := skills.ExpandConfiguredPath(d, cwd, home)
		if raw == "" {
			continue
		}
		exp := filepath.Clean(raw)
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

	loader := skills.NewLoader(s.activeCfg().Skills.SearchDirs())
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

// resolveSessionCWD picks the workspace a cwd-scoped listing describes (skills,
// slash commands, workspace context and files): the cwd of the session named
// by X-Coddy-Session-ID, loading a persisted session on demand, or the server
// default cwd without the header. A malformed id is answered with 400 and an
// unknown session with 404; the function writes that response itself and
// reports false so the handler returns.
func (s *Server) resolveSessionCWD(w http.ResponseWriter, r *http.Request) (string, bool) {
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

// resolveListingCWD picks the workspace of a read-only listing the composer
// asks for while the user types (skills, slash commands, mentions and the
// subagents they offer, the file a mention names, the update check of the
// installed skills): the session named by X-Coddy-Session-ID when the server
// has it, exactly as resolveSessionCWD does, else the folder in the cwd query
// parameter, else the server default cwd. The cwd parameter is how a new chat
// names the folder picked on the start screen before its session exists; the
// SPA sends it next to the header as well, which covers the first send of a
// chat with no folder picked, whose id the server only learns from that turn.
// It must be an absolute path to an existing directory (400 otherwise) and is
// ignored when the session exists, whose own workspace always wins. A query
// and not a header, because a folder name may not be ASCII.
//
// Routes that change a workspace (MCP declarations, trust, marketplaces,
// folder creation) stay on resolveSessionCWD and never take cwd, with one
// exception: DELETE /coddy/skills/{name} deletes a skill in the workspace
// GET /coddy/skills listed it for, so Settings can delete a project skill of
// a folder picked before its session exists. That grants nothing new: a
// client can already open a session in any existing folder
// (POST /coddy/sessions/{id}/workspace) and delete with its header, the route
// is behind the same CSRF check as every other DELETE, and the delete only
// removes paths inside that workspace's skill folders.
func (s *Server) resolveListingCWD(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("cwd"))
	if raw == "" {
		return s.resolveSessionCWD(w, r)
	}
	if sid := strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID")); sid != "" {
		if session.ValidateFolderSessionID(sid) != nil || s.sessionKnown(sid) {
			return s.resolveSessionCWD(w, r)
		}
	}
	if !filepath.IsAbs(raw) {
		http.Error(w, `{"error":{"message":"cwd must be an absolute path"}}`, http.StatusBadRequest)
		return "", false
	}
	// Clean only normalises the spelling here, it sanitises nothing: the
	// parameter names any folder on purpose (the operator's folder picker
	// reaches the whole disk), it must be absolute and an existing directory,
	// and what is behind it reads, or deletes a skill inside that folder's
	// skill directories (see above).
	abs := filepath.Clean(raw) // nosemgrep: go.lang.security.filepath-clean-misuse.filepath-clean-misuse
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, "folder not found: "+abs), http.StatusBadRequest)
		return "", false
	}
	return abs, true
}

// sessionKnown reports whether the server holds the session, live or on disk.
func (s *Server) sessionKnown(sid string) bool {
	if s.mgr.SessionByID(sid) != nil {
		return true
	}
	fs := s.mgr.FileStore()
	return fs != nil && fs.HasPersistedSnapshot(sid)
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

	cwdAbs, ok := s.resolveListingCWD(w, r)
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

// coddyCommandsGet lists the built-in commands for the composer's "Commands"
// group: the settings commands (/model, /reasoning, /think, /nothink,
// /agent, /plan, /ask, /permissions) with their argument choices for the
// session named by X-Coddy-Session-ID, then the deterministic actions
// (/compact while compaction is enabled, /export, /plugin). None of them runs
// a turn of the model; they are not part of /coddy/slash-commands (skills).
func (s *Server) coddyCommandsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	cfg := s.activeCfg()
	var st *session.State
	if sid := strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID")); sid != "" {
		st = s.mgr.SessionByID(sid)
	}
	items := session.BuiltinCommandRows(cfg, st, session.ActionCommandRows(cfg))
	if prefix := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(r.URL.Query().Get("prefix")), "/")); prefix != "" {
		filtered := items[:0:0]
		for _, it := range items {
			if strings.HasPrefix(it.Name, prefix) {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.commands",
		"items":  items,
	})
}
