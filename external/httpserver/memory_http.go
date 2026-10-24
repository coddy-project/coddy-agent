//go:build http && memory

package httpserver

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"

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

// memoryStore is the notes store of a session: the global root of the live
// configuration and the project root of the session's folder, reached the
// way the memory subagent's tools reach them (external/memory/storage), so a
// link in the project root leads nowhere here either.
func (s *Server) memoryStore(st *session.State) (*memstorage.Store, error) {
	cfg := s.activeCfg()
	return memstorage.NewStore(&cfg.Memory, cfg.Paths, st.GetCWD())
}

// memoryScope is the store's scope for a root the API names: "global" or
// "workspace", the project root.
func memoryScope(root string) (string, bool) {
	switch root {
	case "global":
		return "global", true
	case "workspace":
		return "project", true
	default:
		return "", false
	}
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
	return filepath.ToSlash(strings.Trim(cl, `\`)), nil
}

// memoryRequest is what every notes route reads first: the session, its
// store, the scope of the root asked for and the path inside it. It answers
// the request itself and reports ok=false when the caller must stop.
func (s *Server) memoryRequest(w http.ResponseWriter, r *http.Request, root, rawPath string) (store *memstorage.Store, scope, rel string, ok bool) {
	st := s.coddyEnsureLoaded(w, r, strings.TrimSpace(r.PathValue("id")))
	if st == nil {
		return nil, "", "", false
	}
	scope, valid := memoryScope(strings.TrimSpace(root))
	if !valid {
		http.Error(w, `{"error":{"message":"invalid root"}}`, http.StatusBadRequest)
		return nil, "", "", false
	}
	rel, err := memoryRelPath(rawPath)
	if err != nil {
		http.Error(w, errorJSON(err.Error()), http.StatusBadRequest)
		return nil, "", "", false
	}
	store, err = s.memoryStore(st)
	if err != nil {
		http.Error(w, errorJSON(err.Error()), http.StatusInternalServerError)
		return nil, "", "", false
	}
	return store, scope, rel, true
}

// memoryStoreError answers an error of the store: what the request named
// wrong is 400, what is not there 404, anything else the fallback.
func memoryStoreError(w http.ResponseWriter, err error, fallbackCode int, fallback string) {
	switch {
	case errors.Is(err, memstorage.ErrInvalidPath), errors.Is(err, memstorage.ErrNotNote),
		errors.Is(err, memstorage.ErrNotDir), errors.Is(err, memstorage.ErrScopeRoot),
		errors.Is(err, memstorage.ErrProjectRootLink), errors.Is(err, memstorage.ErrOutsideRoot):
		http.Error(w, errorJSON(err.Error()), http.StatusBadRequest)
	case errors.Is(err, fs.ErrNotExist):
		http.Error(w, `{"error":{"message":"not found"}}`, http.StatusNotFound)
	default:
		http.Error(w, errorJSON(fallback), fallbackCode)
	}
}

func (s *Server) coddyMemoryTree(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	rootQ := strings.TrimSpace(r.URL.Query().Get("root"))
	if rootQ == "" {
		st := s.coddyEnsureLoaded(w, r, strings.TrimSpace(r.PathValue("id")))
		if st == nil {
			return
		}
		out := []map[string]string{}
		if store, err := s.memoryStore(st); err == nil && store.GlobalRoot() != "" {
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
	store, scope, rel, ok := s.memoryRequest(w, r, rootQ, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	nodes, err := store.List(scope, rel)
	if err != nil {
		memoryStoreError(w, err, http.StatusInternalServerError, "list failed")
		return
	}
	if nodes == nil {
		nodes = []memstorage.ListEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.memory_tree",
		"root":   rootQ,
		"path":   rel,
		"nodes":  nodes,
	})
}

func (s *Server) coddyMemoryFileGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	root := strings.TrimSpace(r.URL.Query().Get("root"))
	relPath := strings.TrimSpace(r.URL.Query().Get("path"))
	store, scope, rel, ok := s.memoryRequest(w, r, root, relPath)
	if !ok {
		return
	}
	b, err := store.ReadNote(scope, rel)
	if err != nil {
		memoryStoreError(w, err, http.StatusInternalServerError, "read failed")
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
	var body struct {
		Root    string `json:"root"`
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	store, scope, rel, ok := s.memoryRequest(w, r, body.Root, body.Path)
	if !ok {
		return
	}
	if err := store.PutFile(scope, rel, []byte(body.Content)); err != nil {
		memoryStoreError(w, err, http.StatusInternalServerError, "write failed")
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
	var body struct {
		Root string `json:"root"`
		Path string `json:"path"` // subdirectory under allowed root (created)
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	store, scope, rel, ok := s.memoryRequest(w, r, body.Root, body.Path)
	if !ok {
		return
	}
	if err := store.Mkdir(scope, rel); err != nil {
		memoryStoreError(w, err, http.StatusInternalServerError, "mkdir failed")
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
	store, scope, rel, ok := s.memoryRequest(w, r, r.URL.Query().Get("root"), r.URL.Query().Get("path"))
	if !ok {
		return
	}
	if err := store.Delete(scope + ":" + rel); err != nil {
		memoryStoreError(w, err, http.StatusInternalServerError, "delete failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.memory_file_deleted"})
}
