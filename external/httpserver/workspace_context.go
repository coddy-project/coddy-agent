//go:build http

package httpserver

// Workspace context endpoints back the SPA composer chips: current folder,
// git branch, and worktree state, plus folder browsing and switching.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// workspaceContextPayload builds the JSON body shared by the context GET and
// the workspace switch POST.
func workspaceContextPayload(cwd string) map[string]interface{} {
	info := gitws.Describe(cwd)
	payload := map[string]interface{}{
		"object":      "coddy.workspace_context",
		"path":        info.Path,
		"name":        filepath.Base(info.Path),
		"is_git_repo": info.IsGitRepo,
		"is_worktree": info.IsWorktree,
	}
	if info.IsGitRepo {
		payload["repo_root"] = info.RepoRoot
		payload["branch"] = info.Branch
		payload["branches"] = info.Branches
		wts := make([]map[string]interface{}, 0, len(info.Worktrees))
		for _, wt := range info.Worktrees {
			wts = append(wts, map[string]interface{}{
				"path":   wt.Path,
				"branch": wt.Branch,
				"main":   wt.Main,
			})
		}
		payload["worktrees"] = wts
	}
	return payload
}

// coddyWorkspaceContextGet reports the workspace state for ?path= when given
// (pre-session preview), otherwise for the session in X-Coddy-Session-ID
// (or the server default cwd without the header).
func (s *Server) coddyWorkspaceContextGet(w http.ResponseWriter, r *http.Request) {
	cwd := strings.TrimSpace(r.URL.Query().Get("path"))
	if cwd != "" {
		abs, err := filepath.Abs(cwd)
		if err != nil {
			http.Error(w, `{"error":{"message":"invalid path"}}`, http.StatusBadRequest)
			return
		}
		fi, err := os.Stat(abs)
		if err != nil || !fi.IsDir() {
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, "folder not found: "+abs), http.StatusBadRequest)
			return
		}
		cwd = abs
	} else {
		resolved, ok := s.resolveSlashListCWD(w, r)
		if !ok {
			return
		}
		cwd = resolved
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(workspaceContextPayload(cwd))
}

// coddyWorkspaceFoldersGet lists subfolders of ?path= (default: session cwd)
// for the workspace folder picker. Hidden folders and node_modules are skipped.
func (s *Server) coddyWorkspaceFoldersGet(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(r.URL.Query().Get("path"))
	if dir == "" {
		cwd, ok := s.resolveSlashListCWD(w, r)
		if !ok {
			return
		}
		dir = cwd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		http.Error(w, `{"error":{"message":"invalid path"}}`, http.StatusBadRequest)
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, "folder not found: "+abs), http.StatusBadRequest)
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}
	folders := make([]map[string]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		folders = append(folders, map[string]string{
			"name": name,
			"path": filepath.Join(abs, name),
		})
	}
	sort.Slice(folders, func(i, j int) bool { return folders[i]["name"] < folders[j]["name"] })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":  "coddy.workspace_folders",
		"path":    abs,
		"parent":  filepath.Dir(abs),
		"folders": folders,
	})
}

// coddySessionWorkspacePost switches the session workspace: {"path": dir}
// changes the folder, {"branch": b} checks the branch out in place, and
// {"branch": b, "worktree": true} ensures a dedicated worktree for it.
func (s *Server) coddySessionWorkspacePost(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}
	var body struct {
		Path     string `json:"path"`
		Branch   string `json:"branch"`
		Worktree bool   `json:"worktree"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	st, err := s.mgr.EnsureHTTPSession(r.Context(), id, s.defaultCWD)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}

	switch {
	case strings.TrimSpace(body.Path) != "":
		if err := s.mgr.SetSessionWorkspace(st, body.Path); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
			return
		}
	case strings.TrimSpace(body.Branch) != "":
		if status, err := s.applyBranchSwitch(st, body.Branch, body.Worktree); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), status)
			return
		}
	default:
		http.Error(w, `{"error":{"message":"path or branch required"}}`, http.StatusBadRequest)
		return
	}

	payload := workspaceContextPayload(st.GetCWD())
	payload["id"] = id
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// applyBranchSwitch moves the session to branch. A branch already checked out
// in another worktree (including the main one) switches the session cwd there;
// otherwise it is either checked out in place or opened in a new worktree
// under <home>/worktrees/<repo>/. Returns the HTTP status for errors.
func (s *Server) applyBranchSwitch(st *session.State, branch string, useWorktree bool) (int, error) {
	cwd := st.GetCWD()
	info := gitws.Describe(cwd)
	if !info.IsGitRepo {
		return http.StatusBadRequest, fmt.Errorf("workspace is not a git repository: %s", cwd)
	}
	branch = strings.TrimSpace(branch)
	if branch == info.Branch {
		return 0, nil
	}
	for _, wt := range info.Worktrees {
		if wt.Branch == branch {
			if err := s.mgr.SetSessionWorkspace(st, wt.Path); err != nil {
				return http.StatusBadRequest, err
			}
			return 0, nil
		}
	}
	if useWorktree {
		root := filepath.Join(info.RepoRoot, ".coddy", "worktrees")
		if cfg := s.activeCfg(); cfg != nil && strings.TrimSpace(cfg.Paths.Home) != "" {
			root = filepath.Join(cfg.Paths.Home, "worktrees", filepath.Base(info.RepoRoot))
		}
		path, _, err := gitws.EnsureWorktree(info.RepoRoot, branch, root)
		if err != nil {
			return http.StatusConflict, err
		}
		if err := s.mgr.SetSessionWorkspace(st, path); err != nil {
			return http.StatusBadRequest, err
		}
		return 0, nil
	}
	if err := gitws.Checkout(cwd, branch); err != nil {
		return http.StatusConflict, err
	}
	return 0, nil
}
