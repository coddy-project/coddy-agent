//go:build http

package httpserver

// Godog harness for features/workspace_switching.feature: drives the live
// HTTP surface (workspace context, folder browsing, branch/worktree switch).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type wsFeatureState struct {
	root      string
	sessRoot  string
	ts        *httptest.Server
	mgr       *session.Manager
	srv       *Server
	folders   map[string]string
	sessionID string
	status    int
	body      map[string]interface{}
}

func (s *wsFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-ws-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sessRoot = filepath.Join(root, "sessions")
	s.folders = map[string]string{}
	s.sessionID = ""
	s.status = 0
	s.body = nil
	return nil
}

func (s *wsFeatureState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func bddGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %w\n%s", args, err, out)
	}
	return nil
}

func bddNormPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func bddSplitList(list string) []string {
	parts := strings.Split(list, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (s *wsFeatureState) startServer() error {
	home := filepath.Join(s.root, "home")
	if err := os.MkdirAll(filepath.Join(home, "memory"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(s.sessRoot, 0o755); err != nil {
		return err
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: s.root},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	store := &session.FileStore{Root: s.sessRoot}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.root, store)
	s.srv = New(cfg, s.mgr, slog.Default(), s.root)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *wsFeatureState) plainFolder(name string) error {
	dir := filepath.Join(s.root, "ws", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	s.folders[name] = dir
	return nil
}

func (s *wsFeatureState) folderWithSubfolders(name, list string) error {
	if err := s.plainFolder(name); err != nil {
		return err
	}
	for _, sub := range bddSplitList(list) {
		if err := os.MkdirAll(filepath.Join(s.folders[name], sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (s *wsFeatureState) gitRepo(name, branchList string) error {
	if !gitws.GitAvailable() {
		return fmt.Errorf("git binary not available for BDD git scenarios")
	}
	if err := s.plainFolder(name); err != nil {
		return err
	}
	dir := s.folders[name]
	branches := bddSplitList(branchList)
	if len(branches) == 0 {
		branches = []string{"main"}
	}
	if err := bddGit(dir, "init", "-b", branches[0]); err != nil {
		return err
	}
	if err := bddGit(dir, "-c", "user.email=coddy@test", "-c", "user.name=coddy",
		"commit", "--allow-empty", "-m", "init"); err != nil {
		return err
	}
	for _, b := range branches[1:] {
		if err := bddGit(dir, "branch", b); err != nil {
			return err
		}
	}
	return nil
}

func (s *wsFeatureState) sessionRootedAt(name string) error {
	dir, ok := s.folders[name]
	if !ok {
		return fmt.Errorf("unknown folder %q", name)
	}
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: dir})
	if err != nil {
		return err
	}
	s.sessionID = res.SessionID
	return nil
}

func (s *wsFeatureState) sessionHasUserMessage() error {
	st := s.mgr.SessionByID(s.sessionID)
	if st == nil {
		return fmt.Errorf("session %q not registered", s.sessionID)
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hi"})
	return nil
}

func (s *wsFeatureState) do(req *http.Request) error {
	if s.sessionID != "" {
		req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	s.status = res.StatusCode
	s.body = nil
	var parsed map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err == nil {
		s.body = parsed
	}
	return nil
}

func (s *wsFeatureState) requestContext() error {
	req, err := http.NewRequest(http.MethodGet, s.ts.URL+"/coddy/workspace/context", nil)
	if err != nil {
		return err
	}
	return s.do(req)
}

func (s *wsFeatureState) postWorkspace(payload map[string]interface{}) error {
	if s.sessionID == "" {
		return fmt.Errorf("no session created")
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost,
		s.ts.URL+"/coddy/sessions/"+s.sessionID+"/workspace", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return s.do(req)
}

func (s *wsFeatureState) switchToFolder(name string) error {
	dir, ok := s.folders[name]
	if !ok {
		return fmt.Errorf("unknown folder %q", name)
	}
	return s.postWorkspace(map[string]interface{}{"path": dir})
}

func (s *wsFeatureState) switchToMissingFolder() error {
	return s.postWorkspace(map[string]interface{}{
		"path": filepath.Join(s.root, "definitely", "missing"),
	})
}

func (s *wsFeatureState) switchToBranch(branch string) error {
	return s.postWorkspace(map[string]interface{}{"branch": branch})
}

func (s *wsFeatureState) switchToBranchInWorktree(branch string) error {
	return s.postWorkspace(map[string]interface{}{"branch": branch, "worktree": true})
}

func (s *wsFeatureState) alreadySwitchedToWorktree(branch string) error {
	if err := s.switchToBranchInWorktree(branch); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("worktree switch failed with status %d: %v", s.status, s.body)
	}
	return nil
}

func (s *wsFeatureState) browseFolders(name string) error {
	dir, ok := s.folders[name]
	if !ok {
		return fmt.Errorf("unknown folder %q", name)
	}
	req, err := http.NewRequest(http.MethodGet,
		s.ts.URL+"/coddy/workspace/folders?path="+dir, nil)
	if err != nil {
		return err
	}
	return s.do(req)
}

// freshContext re-fetches the workspace context so Then-steps always assert
// against the live session state, not a stale switch response.
func (s *wsFeatureState) freshContext() (map[string]interface{}, error) {
	if err := s.requestContext(); err != nil {
		return nil, err
	}
	if s.status != http.StatusOK {
		return nil, fmt.Errorf("workspace context returned %d: %v", s.status, s.body)
	}
	if s.body == nil {
		return nil, fmt.Errorf("workspace context returned no JSON body")
	}
	return s.body, nil
}

func (s *wsFeatureState) contextPathPointsTo(name string) error {
	dir, ok := s.folders[name]
	if !ok {
		return fmt.Errorf("unknown folder %q", name)
	}
	ctxBody, err := s.freshContext()
	if err != nil {
		return err
	}
	got, _ := ctxBody["path"].(string)
	if bddNormPath(got) != bddNormPath(dir) {
		return fmt.Errorf("context path = %q, want %q", got, dir)
	}
	return nil
}

func (s *wsFeatureState) contextNotGitRepo() error {
	ctxBody, err := s.freshContext()
	if err != nil {
		return err
	}
	if isRepo, _ := ctxBody["is_git_repo"].(bool); isRepo {
		return fmt.Errorf("context reports a git repo: %v", ctxBody)
	}
	return nil
}

func (s *wsFeatureState) contextOnBranch(branch string) error {
	ctxBody, err := s.freshContext()
	if err != nil {
		return err
	}
	if isRepo, _ := ctxBody["is_git_repo"].(bool); !isRepo {
		return fmt.Errorf("context does not report a git repo: %v", ctxBody)
	}
	if got, _ := ctxBody["branch"].(string); got != branch {
		return fmt.Errorf("context branch = %q, want %q", ctxBody["branch"], branch)
	}
	return nil
}

func (s *wsFeatureState) contextListsBranches(list string) error {
	ctxBody, err := s.freshContext()
	if err != nil {
		return err
	}
	raw, _ := ctxBody["branches"].([]interface{})
	have := map[string]bool{}
	for _, b := range raw {
		if v, ok := b.(string); ok {
			have[v] = true
		}
	}
	for _, want := range bddSplitList(list) {
		if !have[want] {
			return fmt.Errorf("branch %q missing in %v", want, raw)
		}
	}
	return nil
}

func (s *wsFeatureState) contextWorktreeFlag(not string) error {
	ctxBody, err := s.freshContext()
	if err != nil {
		return err
	}
	isWT, _ := ctxBody["is_worktree"].(bool)
	wantWT := not == ""
	if isWT != wantWT {
		return fmt.Errorf("is_worktree = %v, want %v", isWT, wantWT)
	}
	return nil
}

func (s *wsFeatureState) worktreePathDiffersFromRoot() error {
	ctxBody, err := s.freshContext()
	if err != nil {
		return err
	}
	path, _ := ctxBody["path"].(string)
	root, _ := ctxBody["repo_root"].(string)
	if path == "" || root == "" {
		return fmt.Errorf("context misses path/repo_root: %v", ctxBody)
	}
	if bddNormPath(path) == bddNormPath(root) {
		return fmt.Errorf("worktree path %q equals repository root", path)
	}
	return nil
}

func (s *wsFeatureState) sessionCwdPersistedAs(name string) error {
	dir, ok := s.folders[name]
	if !ok {
		return fmt.Errorf("unknown folder %q", name)
	}
	raw, err := os.ReadFile(filepath.Join(s.sessRoot, s.sessionID, "session.json"))
	if err != nil {
		return err
	}
	var meta struct {
		CWD string `json:"cwd"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return err
	}
	if bddNormPath(meta.CWD) != bddNormPath(dir) {
		return fmt.Errorf("persisted cwd = %q, want %q", meta.CWD, dir)
	}
	return nil
}

func (s *wsFeatureState) requestFailsWithStatus(code int) error {
	if s.status != code {
		return fmt.Errorf("status = %d, want %d (body: %v)", s.status, code, s.body)
	}
	return nil
}

func (s *wsFeatureState) folderListingContains(list string) error {
	if s.status != http.StatusOK {
		return fmt.Errorf("folder listing returned %d: %v", s.status, s.body)
	}
	raw, _ := s.body["folders"].([]interface{})
	have := map[string]bool{}
	for _, f := range raw {
		if m, ok := f.(map[string]interface{}); ok {
			if n, ok := m["name"].(string); ok {
				have[n] = true
			}
		}
	}
	for _, want := range bddSplitList(list) {
		if !have[want] {
			return fmt.Errorf("folder %q missing in %v", want, raw)
		}
	}
	return nil
}

func initializeWorkspaceScenario(sc *godog.ScenarioContext) {
	s := &wsFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy HTTP server$`, s.startServer)
	sc.Step(`^a workspace folder "([^"]+)" without git$`, s.plainFolder)
	sc.Step(`^a workspace folder "([^"]+)" containing subfolders "([^"]+)"$`, s.folderWithSubfolders)
	sc.Step(`^a workspace git repository "([^"]+)" with branches "([^"]+)"$`, s.gitRepo)
	sc.Step(`^a session rooted at folder "([^"]+)"$`, s.sessionRootedAt)
	sc.Step(`^the session switched to branch "([^"]+)" in a worktree$`, s.alreadySwitchedToWorktree)
	sc.Step(`^the session already has a user message$`, s.sessionHasUserMessage)

	sc.Step(`^I request the workspace context$`, s.requestContext)
	sc.Step(`^I switch the session workspace to folder "([^"]+)"$`, s.switchToFolder)
	sc.Step(`^I switch the session workspace to a folder that does not exist$`, s.switchToMissingFolder)
	sc.Step(`^I switch the session to branch "([^"]+)" in a worktree$`, s.switchToBranchInWorktree)
	sc.Step(`^I switch the session to branch "([^"]+)"$`, s.switchToBranch)
	sc.Step(`^I browse workspace folders under "([^"]+)"$`, s.browseFolders)

	sc.Step(`^the context path points to folder "([^"]+)"$`, s.contextPathPointsTo)
	sc.Step(`^the context reports it is not a git repository$`, s.contextNotGitRepo)
	sc.Step(`^the context reports a git repository on branch "([^"]+)"$`, s.contextOnBranch)
	sc.Step(`^the context lists branches "([^"]+)"$`, s.contextListsBranches)
	sc.Step(`^the context reports the session is (not )?in a worktree$`, s.contextWorktreeFlag)
	sc.Step(`^the worktree path differs from the repository root$`, s.worktreePathDiffersFromRoot)
	sc.Step(`^the session cwd is persisted as folder "([^"]+)"$`, s.sessionCwdPersistedAs)
	sc.Step(`^the workspace request fails with status (\d+)$`, s.requestFailsWithStatus)
	sc.Step(`^the folder listing contains "([^"]+)"$`, s.folderListingContains)
}

func TestWorkspaceSwitchingFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "workspace-switching",
		ScenarioInitializer: initializeWorkspaceScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features/workspace_switching.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("workspace switching feature suite failed")
	}
}
