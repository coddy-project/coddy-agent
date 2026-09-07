//go:build http

package httpserver

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const workspaceFilesMaxEntries = 50000

func workspaceSkipSubtree(name string) bool {
	switch name {
	case ".git", "node_modules", ".coddy":
		return true
	default:
		return false
	}
}

type workspaceListedItem struct {
	Name    string `json:"name"`
	PathRel string `json:"path_rel"`
	Kind    string `json:"kind"`
}

func collectWorkspaceListedItems(root string, includeDirs bool) ([]workspaceListedItem, error) {
	root = filepath.Clean(root)
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var entries []workspaceListedItem
	errWalk := filepath.WalkDir(rootAbs, func(path string, de fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if len(entries) >= workspaceFilesMaxEntries {
			if de.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		name := filepath.Base(path)
		if path == rootAbs {
			return nil
		}
		if workspaceSkipSubtree(name) {
			if de.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		relSlash, relErr := filepath.Rel(rootAbs, path)
		if relErr != nil {
			return relErr
		}
		relSlash = filepath.ToSlash(relSlash)

		switch {
		case de.IsDir():
			if includeDirs {
				if len(entries) >= workspaceFilesMaxEntries {
					return filepath.SkipDir
				}
				entries = append(entries, workspaceListedItem{
					Name:    name,
					PathRel: relSlash + "/",
					Kind:    "dir",
				})
			}
			return nil
		default:
			if len(entries) >= workspaceFilesMaxEntries {
				return nil
			}
			entries = append(entries, workspaceListedItem{
				Name:    name,
				PathRel: relSlash,
				Kind:    "file",
			})
			return nil
		}
	})
	if errWalk != nil {
		return nil, errWalk
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].PathRel < entries[j].PathRel
	})
	return entries, nil
}

func paginateWorkspaceItems(items []workspaceListedItem, page, pageSize int) (pageItems []workspaceListedItem, total int, hasMore bool) {
	total = len(items)
	if page < 1 {
		page = 1
	}
	start := (page - 1) * pageSize
	if start >= total {
		return []workspaceListedItem{}, total, false
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return items[start:end], total, end < total
}

func (s *Server) coddyWorkspaceFilesGet(w http.ResponseWriter, r *http.Request) {
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

	includeDirs := false
	if v := strings.TrimSpace(strings.ToLower(q.Get("dirs"))); v == "1" || v == "true" || v == "yes" {
		includeDirs = true
	}

	cwdAbs, ok := s.resolveSkillsCWD(w, r)
	if !ok {
		return
	}

	prefix := strings.TrimSpace(q.Get("prefix"))
	if prefix == "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object":    "coddy.workspace_files_page",
			"items":     []workspaceListedItem{},
			"total":     0,
			"has_more":  false,
			"page":      page,
			"page_size": pageSize,
		})
		return
	}

	st, err := os.Stat(cwdAbs)
	if err != nil || !st.IsDir() {
		http.Error(w, `{"error":{"message":"invalid session cwd"}}`, http.StatusInternalServerError)
		return
	}

	all, err := collectWorkspaceListedItems(cwdAbs, includeDirs)
	if err != nil {
		http.Error(w, `{"error":{"message":"failed to list workspace files"}}`, http.StatusInternalServerError)
		return
	}

	prefixLower := strings.ToLower(prefix)
	var filtered []workspaceListedItem
	for _, it := range all {
		if strings.Contains(strings.ToLower(it.PathRel), prefixLower) {
			filtered = append(filtered, it)
		}
	}

	pageItems, total, hasMore := paginateWorkspaceItems(filtered, page, pageSize)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":    "coddy.workspace_files_page",
		"items":     pageItems,
		"total":     total,
		"has_more":  hasMore,
		"page":      page,
		"page_size": pageSize,
	})
}
