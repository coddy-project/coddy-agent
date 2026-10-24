//go:build http && memory

package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	memstorage "github.com/EvilFreelancer/coddy-agent/external/memory/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

var errTraversal = errors.New("path escapes allowed root")

func mergeOpenAPIMemoryDoc(_ *map[string]interface{}) {
	// Memory REST routes are served live; extend this merge when paths are added to openAPISpec.
}

func (s *Server) registerMemoryRoutes() {
	s.mux.HandleFunc("GET /coddy/sessions/{id}/memory/tree", s.coddyMemoryTree)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/memory/file", s.coddyMemoryFileGet)
	s.mux.HandleFunc("PUT /coddy/sessions/{id}/memory/file", s.coddyMemoryFilePut)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/memory/dir", s.coddyMemoryDirPost)
	s.mux.HandleFunc("DELETE /coddy/sessions/{id}/memory/file", s.coddyMemoryFileDelete)
}

func (s *Server) coddyPaths() pathsForMemoryAPI {
	dir := strings.TrimSpace(s.activeCfg().Memory.Dir)
	var globalRoot string
	if dir != "" {
		globalRoot = filepath.Clean(dir)
	} else if h := strings.TrimSpace(s.activeCfg().Paths.Home); h != "" {
		globalRoot = filepath.Join(h, "memory")
	}
	return pathsForMemoryAPI{globalMemoryRoot: globalRoot}
}

type pathsForMemoryAPI struct {
	globalMemoryRoot string
}

func memoryRelPath(p string) (string, error) {
	raw := filepath.ToSlash(strings.TrimSpace(p))
	if raw == "" {
		return "", nil
	}
	if strings.Contains(raw, "..") {
		return "", errTraversal
	}
	cl := filepath.Clean(raw)
	if cl == "." {
		return "", nil
	}
	if filepath.IsAbs(cl) {
		return "", errTraversal
	}
	for _, seg := range strings.Split(filepath.ToSlash(cl), "/") {
		if seg == ".." || seg == "" {
			return "", errTraversal
		}
	}
	return filepath.FromSlash(strings.Trim(cl, `\`)), nil
}

func absUnder(root, rel string) (string, error) {
	rootClean, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	joined := filepath.Join(rootClean, rel)
	target, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	relCmp, err := filepath.Rel(rootClean, target)
	if err != nil {
		return "", errTraversal
	}
	for _, seg := range strings.Split(relCmp, string(filepath.Separator)) {
		if seg == ".." {
			return "", errTraversal
		}
	}
	return target, nil
}

func (s *Server) coddyMemoryTree(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	p := s.coddyPaths()
	rootQ := strings.TrimSpace(r.URL.Query().Get("root"))
	if rootQ == "" {
		out := []map[string]string{}
		if p.globalMemoryRoot != "" {
			out = append(out, map[string]string{"id": "global", "path": ""})
		}
		out = append(out, map[string]string{"id": "workspace", "path": ""})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "coddy.memory_roots",
			"roots":  out,
		})
		return
	}
	wsRoot := memstorage.ProjectRootFor(st.GetCWD())
	var base string
	switch rootQ {
	case "global":
		if p.globalMemoryRoot == "" {
			http.Error(w, `{"error":{"message":"global memory root not configured"}}`, http.StatusBadRequest)
			return
		}
		base = p.globalMemoryRoot
	case "workspace":
		base = wsRoot
	default:
		http.Error(w, `{"error":{"message":"invalid root"}}`, http.StatusBadRequest)
		return
	}
	rel, err := memoryRelPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, errorJSON(err.Error()), http.StatusBadRequest)
		return
	}
	dirAbs, err := absUnder(base, rel)
	if err != nil {
		http.Error(w, `{"error":{"message":"bad path"}}`, http.StatusBadRequest)
		return
	}
	fi, err := os.Stat(dirAbs)
	if err != nil {
		http.Error(w, `{"error":{"message":"not found"}}`, http.StatusNotFound)
		return
	}
	if !fi.IsDir() {
		http.Error(w, `{"error":{"message":"not a directory"}}`, http.StatusBadRequest)
		return
	}
	de, err := os.ReadDir(dirAbs)
	if err != nil {
		http.Error(w, `{"error":{"message":"list failed"}}`, http.StatusInternalServerError)
		return
	}
	type node struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		Size     int64  `json:"size,omitempty"`
		Modified string `json:"modified,omitempty"`
	}
	nodes := make([]node, 0)
	for _, e := range de {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.IsDir() {
			nodes = append(nodes, node{Name: name, Kind: "dir", Modified: info.ModTime().UTC().Format(time.RFC3339)})
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".md" && ext != ".txt" {
			continue
		}
		nodes = append(nodes, node{Name: name, Kind: "file", Size: info.Size(), Modified: info.ModTime().UTC().Format(time.RFC3339)})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.memory_tree",
		"root":   rootQ,
		"path":   filepath.ToSlash(rel),
		"nodes":  nodes,
	})
}

func (s *Server) coddyResolveMemoryAbs(st *session.State, rootKind, rel string) (abs string, err error) {
	p := s.coddyPaths()
	var base string
	switch rootKind {
	case "global":
		if p.globalMemoryRoot == "" {
			return "", fmt.Errorf("global memory root unavailable")
		}
		base = p.globalMemoryRoot
	case "workspace":
		base = memstorage.ProjectRootFor(st.GetCWD())
	default:
		return "", fmt.Errorf("invalid root")
	}
	relSan, terr := memoryRelPath(rel)
	if terr != nil {
		return "", terr
	}
	return absUnder(base, relSan)
}

func (s *Server) coddyMemoryFileGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	root := strings.TrimSpace(r.URL.Query().Get("root"))
	relPath := strings.TrimSpace(r.URL.Query().Get("path"))
	abs, err := s.coddyResolveMemoryAbs(st, root, relPath)
	if err != nil {
		http.Error(w, errorJSON(err.Error()), http.StatusBadRequest)
		return
	}
	stat, err := os.Stat(abs) //nolint:gosec // G703: confined to the session's memory root by coddyResolveMemoryAbs (absUnder)
	if err != nil || stat.IsDir() {
		http.Error(w, `{"error":{"message":"not found"}}`, http.StatusNotFound)
		return
	}
	ext := strings.ToLower(filepath.Ext(abs))
	if ext != ".md" && ext != ".txt" {
		http.Error(w, `{"error":{"message":"unsupported extension"}}`, http.StatusBadRequest)
		return
	}
	b, err := os.ReadFile(abs) //nolint:gosec // G703: confined to the session's memory root by coddyResolveMemoryAbs (absUnder)
	if err != nil {
		http.Error(w, `{"error":{"message":"read failed"}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":  "coddy.memory_file",
		"root":    root,
		"path":    filepath.ToSlash(relPath),
		"content": string(b),
	})
}

func (s *Server) coddyMemoryFilePut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	var body struct {
		Root    string `json:"root"`
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	root := strings.TrimSpace(body.Root)
	p := strings.TrimSpace(body.Path)
	abs, err := s.coddyResolveMemoryAbs(st, root, p)
	if err != nil {
		http.Error(w, errorJSON(err.Error()), http.StatusBadRequest)
		return
	}
	ext := strings.ToLower(filepath.Ext(abs))
	if ext != ".md" && ext != ".txt" {
		http.Error(w, `{"error":{"message":"unsupported extension"}}`, http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		http.Error(w, `{"error":{"message":"mkdir failed"}}`, http.StatusInternalServerError)
		return
	}
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, []byte(body.Content), 0o644); err != nil {
		http.Error(w, `{"error":{"message":"write failed"}}`, http.StatusInternalServerError)
		return
	}
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		http.Error(w, `{"error":{"message":"rename failed"}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.memory_file_saved"})
}

func (s *Server) coddyMemoryDirPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	var body struct {
		Root string `json:"root"`
		Path string `json:"path"` // subdirectory under allowed root (created)
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	abs, err := s.coddyResolveMemoryAbs(st, strings.TrimSpace(body.Root), strings.TrimSpace(body.Path))
	if err != nil {
		http.Error(w, errorJSON(err.Error()), http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		http.Error(w, errorJSON(err.Error()), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.memory_dir_created"})
}

func (s *Server) coddyMemoryFileDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	root := strings.TrimSpace(r.URL.Query().Get("root"))
	relPath := strings.TrimSpace(r.URL.Query().Get("path"))
	abs, err := s.coddyResolveMemoryAbs(st, root, relPath)
	if err != nil {
		http.Error(w, errorJSON(err.Error()), http.StatusBadRequest)
		return
	}
	p := s.coddyPaths()
	var memRoot string
	switch root {
	case "global":
		memRoot = p.globalMemoryRoot
	case "workspace":
		memRoot = memstorage.ProjectRootFor(st.GetCWD())
	default:
		http.Error(w, `{"error":{"message":"invalid root"}}`, http.StatusBadRequest)
		return
	}
	if memRoot == "" {
		http.Error(w, `{"error":{"message":"cannot delete memory root"}}`, http.StatusBadRequest)
		return
	}
	rootAbs, err := absUnder(memRoot, "")
	if err != nil {
		http.Error(w, `{"error":{"message":"memory root unavailable"}}`, http.StatusInternalServerError)
		return
	}
	if abs == rootAbs {
		http.Error(w, `{"error":{"message":"cannot delete memory root"}}`, http.StatusBadRequest)
		return
	}
	stat, err := os.Stat(abs) //nolint:gosec // G703: confined to the session's memory root by coddyResolveMemoryAbs (absUnder)
	if err != nil {
		http.Error(w, `{"error":{"message":"not found"}}`, http.StatusNotFound)
		return
	}
	var delErr error
	if stat.IsDir() {
		delErr = os.RemoveAll(abs) //nolint:gosec // G703: confined to the session's memory root by coddyResolveMemoryAbs (absUnder)
	} else {
		delErr = os.Remove(abs) //nolint:gosec // G703: confined to the session's memory root by coddyResolveMemoryAbs (absUnder)
	}
	if delErr != nil {
		http.Error(w, `{"error":{"message":"delete failed"}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.memory_file_deleted"})
}
