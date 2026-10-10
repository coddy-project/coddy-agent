//go:build http

package httpserver

// Workspace context endpoints back the SPA composer chips: current folder,
// git branch, and worktree state, plus folder browsing and switching.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
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
		// The interpreter run_command goes through. It belongs to the machine, not to
		// the folder, but this is the host fact the SPA already asks for, and the tool
		// card names the shell rather than calling everything "Shell".
		"shell": platform.CurrentShell().Path,
	}
	if info.IsGitRepo {
		payload["repo_root"] = info.RepoRoot
		if info.BaseBranch != "" {
			payload["base_branch"] = info.BaseBranch
		}
		payload["branch"] = info.Branch
		payload["branches"] = info.Branches
		// As the last fetch left them: POST /coddy/workspace/fetch refreshes
		// them when the branch list opens.
		payload["remote_branches"] = gitws.RemoteBranches(info.Path)
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
	cwd, ok := s.workspaceContextCWD(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(workspaceContextPayload(cwd))
}

// workspaceContextCWD resolves the folder a workspace context call is about:
// ?path= when given, else the session's cwd. It answers 400 itself for a
// path that is not an existing folder.
func (s *Server) workspaceContextCWD(w http.ResponseWriter, r *http.Request) (string, bool) {
	cwd := strings.TrimSpace(r.URL.Query().Get("path"))
	if cwd == "" {
		return s.resolveSessionCWD(w, r)
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		http.Error(w, `{"error":{"message":"invalid path"}}`, http.StatusBadRequest)
		return "", false
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, "folder not found: "+abs), http.StatusBadRequest)
		return "", false
	}
	return abs, true
}

// workspaceFetchTimeout bounds the refresh the branch list waits for: a
// remote that does not answer leaves the cached list on screen with a warning
// instead of an open menu that never fills.
var workspaceFetchTimeout = 20 * time.Second

// coddyWorkspaceFetchPost refreshes the remote branches of a workspace before
// its branch list opens: every configured remote is fetched (pruned, no tags,
// nothing local touched; see gitws.FetchRemotes) and the answer is the
// workspace context read afterwards plus a "fetch" block saying whether the
// refresh happened. A failed refresh still answers 200 with the context as
// it stands, so the list stays usable, and says it is not fresh. The folder is
// chosen like the context GET: ?path=, else the session in
// X-Coddy-Session-ID, else the server default cwd.
func (s *Server) coddyWorkspaceFetchPost(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.workspaceContextCWD(w, r)
	if !ok {
		return
	}
	fetch := workspaceFetch(r.Context(), cwd)
	payload := workspaceContextPayload(cwd)
	payload["fetch"] = fetch
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// workspaceFetch runs the refresh for cwd and describes its outcome:
// "ok" with the remotes fetched and the time, "failed" with the remotes asked
// and the reason, or "skipped" with why there was nothing to fetch.
func workspaceFetch(ctx context.Context, cwd string) map[string]interface{} {
	if !gitws.Describe(cwd).IsGitRepo {
		return map[string]interface{}{"status": "skipped", "reason": "not a git repository"}
	}
	ctx, cancel := context.WithTimeout(ctx, workspaceFetchTimeout)
	defer cancel()
	remotes, err := gitws.FetchRemotes(ctx, cwd)
	if remotes == nil {
		remotes = []string{}
	}
	switch {
	case err != nil:
		msg := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			msg = fmt.Sprintf("the remotes did not answer within %s", workspaceFetchTimeout)
		}
		return map[string]interface{}{"status": "failed", "remotes": remotes, "error": msg}
	case len(remotes) == 0:
		// No remote at all, or only ones marked skipFetchAll.
		return map[string]interface{}{"status": "skipped", "reason": "no remotes to fetch"}
	default:
		return map[string]interface{}{
			"status":     "ok",
			"remotes":    remotes,
			"fetched_at": time.Now().UTC().Format(time.RFC3339),
		}
	}
}

// workspaceDrivesPath is the pseudo-path of the volume level that sits above
// the drive roots ("This PC"). A Windows drive root has no parent directory -
// filepath.Dir(`C:\`) is `C:\` - so the picker needs a synthetic level to hop
// between volumes from. A colon is only legal after the drive letter, so the
// sentinel cannot collide with a real Windows path.
const workspaceDrivesPath = ":drives:"

// drivesListingPayload renders the volume level: one row per drive root, and
// no level above it (path == parent, so the picker hides its ".." row).
func drivesListingPayload(drives []string) map[string]interface{} {
	folders := make([]map[string]interface{}, 0, len(drives))
	for _, root := range drives {
		folders = append(folders, map[string]interface{}{
			"name": strings.TrimRight(root, `\/`),
			"path": root,
		})
	}
	return map[string]interface{}{
		"object":  "coddy.workspace_folders",
		"path":    workspaceDrivesPath,
		"parent":  workspaceDrivesPath,
		"drives":  true,
		"folders": folders,
	}
}

// folderListingPayload renders a real directory. The parent of a filesystem
// root is promoted to the volume level when the host has drives, which is what
// lets a Windows session walk up out of `C:\` and into another drive.
func folderListingPayload(abs string, folders []map[string]interface{}, drives []string) map[string]interface{} {
	parent := filepath.Dir(abs)
	if parent == abs && len(drives) > 0 {
		parent = workspaceDrivesPath
	}
	return map[string]interface{}{
		"object":  "coddy.workspace_folders",
		"path":    abs,
		"parent":  parent,
		"folders": folders,
	}
}

// coddyWorkspaceFoldersGet lists subfolders of ?path= (default: session cwd)
// for the workspace folder picker. node_modules is always skipped; hidden
// folders are included only when ?show_hidden=true. ?path=:drives: lists the
// machine's drive roots instead.
func (s *Server) coddyWorkspaceFoldersGet(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(r.URL.Query().Get("path"))
	drives := s.hostDrives()
	if dir == workspaceDrivesPath && len(drives) > 0 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(drivesListingPayload(drives))
		return
	}
	if dir == "" {
		cwd, ok := s.resolveSessionCWD(w, r)
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
	// Stat above follows a symlink only to verify that its final target is a
	// directory. Resolve it for the listing so a pasted symlink path arrives at
	// the actual directory and cannot be mistaken for a regular folder later.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}
	showHidden := r.URL.Query().Get("show_hidden") == "true"
	folders := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if name == "node_modules" || (!showHidden && strings.HasPrefix(name, ".")) {
			continue
		}
		path := filepath.Join(abs, name)
		row := map[string]interface{}{
			"name": name,
			"path": path,
		}
		if strings.HasPrefix(name, ".") {
			row["hidden"] = true
		}
		if e.Type()&os.ModeSymlink != 0 {
			// Do not expose an arbitrary symlink as a navigable folder. os.Stat
			// follows it only after detecting the link, and accepts it only when
			// its final target is a directory.
			targetInfo, err := os.Stat(path)
			if err != nil || !targetInfo.IsDir() {
				continue
			}
			target, err := filepath.EvalSymlinks(path)
			if err != nil {
				continue
			}
			row["symlink"] = true
			row["target"] = target
		} else if !e.IsDir() {
			continue
		}
		folders = append(folders, row)
	}
	sort.Slice(folders, func(i, j int) bool {
		return folders[i]["name"].(string) < folders[j]["name"].(string)
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(folderListingPayload(abs, folders, drives))
}

// validWorkspaceFolderName reports whether name is a single new directory
// entry rather than a path. The picker creates a direct child of the folder it
// is browsing, so anything that could walk somewhere else - a separator, a
// volume name, "." or ".." - is refused instead of being cleaned up. Both
// separators are rejected on every OS so a name behaves the same everywhere.
func validWorkspaceFolderName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return false
	}
	return name == filepath.Base(name) && filepath.VolumeName(name) == ""
}

// coddyWorkspaceFoldersPost creates one subfolder inside the browsed directory
// and answers with the listing of the folder it just made, so the picker can
// step straight into it and open it as the workspace. Body: {"path","name"}.
func (s *Server) coddyWorkspaceFoldersPost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(body.Name)
	if !validWorkspaceFolderName(name) {
		http.Error(w, `{"error":{"message":"invalid folder name"}}`, http.StatusBadRequest)
		return
	}
	dir := strings.TrimSpace(body.Path)
	// The volume level is synthetic: there is no directory to create anything in.
	if dir == workspaceDrivesPath {
		http.Error(w, `{"error":{"message":"cannot create a folder at the drive level"}}`, http.StatusBadRequest)
		return
	}
	if dir == "" {
		cwd, ok := s.resolveSessionCWD(w, r)
		if !ok {
			return
		}
		dir = cwd
	}
	parent, err := filepath.Abs(dir)
	if err != nil {
		http.Error(w, `{"error":{"message":"invalid path"}}`, http.StatusBadRequest)
		return
	}
	// The parent has to exist already; a picker that silently builds a whole
	// chain from a typo is worse than one that says no.
	fi, err := os.Stat(parent)
	if err != nil || !fi.IsDir() {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, "folder not found: "+parent), http.StatusBadRequest)
		return
	}
	made := filepath.Join(parent, name)
	if err := os.Mkdir(made, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, "folder already exists: "+made), http.StatusConflict)
			return
		}
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(folderListingPayload(made, []map[string]interface{}{}, s.hostDrives()))
}

// hostDrives lists the machine's drive roots, tolerating a server built
// without the seam (zero-value Server in older tests).
func (s *Server) hostDrives() []string {
	if s.drives == nil {
		return nil
	}
	return s.drives()
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
	// A folder pick is validated before the session exists, so a fresh bundle is
	// created straight in the target workspace - with that workspace's skills,
	// hooks and MCP servers - instead of in the server default and switched
	// afterwards. The early check applies only to a session that does not exist
	// yet: for an existing one the lock checks below must answer first, so a
	// locked session keeps its 409 even when the path is bad.
	createCWD := s.defaultCWD
	existing := s.mgr.SessionByID(id) != nil
	if !existing {
		if fs := s.mgr.FileStore(); fs != nil {
			existing = fs.HasPersistedSnapshot(id)
		}
	}
	if p := strings.TrimSpace(body.Path); p != "" && !existing {
		abs, verr := session.ValidateWorkspaceDir(p)
		if verr != nil {
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, verr.Error()), http.StatusBadRequest)
			return
		}
		body.Path = abs
		createCWD = abs
	}
	st, err := s.mgr.EnsureHTTPSession(r.Context(), id, createCWD)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}
	// A child session inherited its workspace from the parent's turn and is
	// read-only; nothing may move it, whatever its message count.
	if rejectSubagentTurn(w, st) {
		return
	}
	// Folder, branch, and worktree are fixed at session start: once the
	// conversation has messages, the workspace no longer moves under it. The
	// message check alone is not enough: a first turn is admitted (its lock
	// taken) before its user message lands, and a switch must not swap the
	// workspace or its MCP servers under a turn already running.
	if len(st.GetMessages()) > 0 {
		http.Error(w, `{"error":{"message":"workspace is locked once the conversation starts"}}`, http.StatusConflict)
		return
	}
	if s.mgr.SessionTurnActiveInProcess(id) {
		http.Error(w, `{"error":{"message":"workspace is locked while a turn is running"}}`, http.StatusConflict)
		return
	}

	switch {
	case strings.TrimSpace(body.Path) != "":
		if err := s.mgr.SetSessionWorkspace(r.Context(), st, body.Path); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
			return
		}
	case strings.TrimSpace(body.Branch) != "":
		if status, err := s.applyBranchSwitch(r.Context(), st, body.Branch, body.Worktree); err != nil {
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
// under the repository's own <repo>/.coddy/worktrees/. Returns the HTTP status
// for errors.
func (s *Server) applyBranchSwitch(ctx context.Context, st *session.State, branch string, useWorktree bool) (int, error) {
	cwd := st.GetCWD()
	info := gitws.Describe(cwd)
	if !info.IsGitRepo {
		return http.StatusBadRequest, fmt.Errorf("workspace is not a git repository: %s", cwd)
	}
	branch = strings.TrimSpace(branch)
	if useWorktree {
		path, _, err := gitws.EnsureWorktree(info.RepoRoot, branch)
		if err != nil {
			return http.StatusConflict, err
		}
		if err := s.mgr.SetSessionWorkspace(ctx, st, path); err != nil {
			return http.StatusBadRequest, err
		}
		return 0, nil
	}
	// A branch of a remote picked by its remote-tracking name
	// (upstream/fix/x) means the local branch of that name, created tracking
	// it when there is none yet.
	target, err := gitws.ResolveBranch(cwd, branch)
	if err != nil {
		return http.StatusConflict, err
	}
	if target.Branch == info.Branch {
		return 0, nil
	}
	for _, wt := range info.Worktrees {
		if wt.Branch == target.Branch {
			if err := s.mgr.SetSessionWorkspace(ctx, st, wt.Path); err != nil {
				return http.StatusBadRequest, err
			}
			return 0, nil
		}
	}
	if err := gitws.CheckoutTracking(cwd, target); err != nil {
		return http.StatusConflict, err
	}
	// The checkout rewrote this workspace's files under the session: project
	// MCP declarations, skills, rules and hook context may all differ now.
	s.mgr.ReloadSessionWorkspace(ctx, st)
	return 0, nil
}
