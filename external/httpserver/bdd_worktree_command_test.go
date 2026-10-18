//go:build http

package httpserver

// Godog harness for features/worktree_command.feature: the built-in
// /worktree command over /v1/responses, the way the web UI sends it, with the
// real agent runner wired to the manager's workspace switch as `coddy serve`
// wires it (the model is never called: the command is deterministic), against
// a repository with a bare origin.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type worktreeCommandState struct {
	root, repo string
	ts         *httptest.Server
	mgr        *session.Manager
	srv        *Server
	sessionID  string
	reply      string
}

func (s *worktreeCommandState) close() {
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

func (s *worktreeCommandState) serverWithChatInRepository() error {
	root, err := os.MkdirTemp("", "coddy-bdd-worktree-command-*")
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	s.root = root
	s.repo = filepath.Join(root, "project")
	origin := filepath.Join(root, "origin.git")
	if err := os.MkdirAll(s.repo, 0o755); err != nil {
		return err
	}
	for _, step := range []struct {
		dir  string
		args []string
	}{
		{s.repo, []string{"init", "-q", "-b", "main"}},
		{s.repo, []string{"-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-q", "--allow-empty", "-m", "initial"}},
		{root, []string{"clone", "-q", "--bare", s.repo, origin}},
		{s.repo, []string{"remote", "add", "origin", origin}},
		{s.repo, []string{"fetch", "-q", "origin"}},
		{s.repo, []string{"remote", "set-head", "origin", "-a"}},
	} {
		if err := bddGit(step.dir, step.args...); err != nil {
			return err
		}
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: s.repo},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	fakeFactory := func(llm.ProviderInput) (llm.Provider, error) { return cannedSummaryProvider{}, nil }
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		ag := agent.NewAgent(cfg, st, snd, slog.Default())
		ag.SetProviderFactory(fakeFactory)
		ag.SetWorkspaceSwitcher(func(ctx context.Context, dir string) error {
			return s.mgr.SetSessionWorkspaceDuringTurn(ctx, st, dir)
		})
		return ag.Run(ctx, prompt)
	}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.repo, &session.FileStore{Root: filepath.Join(root, "sessions")})
	s.srv = New(cfg, s.mgr, slog.Default(), s.repo)
	s.srv.agentProviderFactory = fakeFactory
	s.ts = httptest.NewServer(s.srv.Handler())
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.repo})
	if err != nil {
		return err
	}
	s.sessionID = res.SessionID
	return nil
}

func (s *worktreeCommandState) sends(prompt string) error {
	buf, _ := json.Marshal(map[string]interface{}{"model": "agent", "input": prompt, "stream": false})
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /v1/responses status %d", res.StatusCode)
	}
	var parsed struct {
		Output []struct {
			Text string `json:"text"`
		} `json:"output"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return err
	}
	s.reply = ""
	for _, o := range parsed.Output {
		s.reply += o.Text
	}
	return nil
}

// workspace is what the web UI's plate reads for the chat.
func (s *worktreeCommandState) workspace() (gitws.Info, error) {
	req, err := http.NewRequest(http.MethodGet, s.ts.URL+"/coddy/workspace/context", nil)
	if err != nil {
		return gitws.Info{}, err
	}
	req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return gitws.Info{}, err
	}
	defer func() { _ = res.Body.Close() }()
	var info gitws.Info
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return gitws.Info{}, err
	}
	return info, nil
}

func (s *worktreeCommandState) worktreePath(branch string) string {
	return filepath.Join(gitws.WorktreesRoot(s.repo), gitws.BranchDirName(branch))
}

func (s *worktreeCommandState) worksInWorktree(branch string) error {
	info, err := s.workspace()
	if err != nil {
		return err
	}
	if !info.IsWorktree || info.Branch != branch || !session.SameWorkspacePath(info.Path, s.worktreePath(branch)) {
		return fmt.Errorf("the chat works in %s on %q (worktree %t), want the worktree for %s (reply %q)",
			info.Path, info.Branch, info.IsWorktree, branch, s.reply)
	}
	return nil
}

func (s *worktreeCommandState) replyNamesWorktree(branch string) error {
	if !strings.Contains(s.reply, s.worktreePath(branch)) {
		return fmt.Errorf("reply %q does not name %s", s.reply, s.worktreePath(branch))
	}
	return nil
}

func (s *worktreeCommandState) worksInMainCheckout() error {
	info, err := s.workspace()
	if err != nil {
		return err
	}
	if info.IsWorktree || !session.SameWorkspacePath(info.Path, s.repo) {
		return fmt.Errorf("the chat works in %s (worktree %t), want the main checkout %s (reply %q)", info.Path, info.IsWorktree, s.repo, s.reply)
	}
	return nil
}

func (s *worktreeCommandState) commandInTranscript(command string) error {
	st := s.mgr.SessionByID(s.sessionID)
	if st == nil {
		return fmt.Errorf("session not found")
	}
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleUser && strings.TrimSpace(m.Content) == command {
			return nil
		}
	}
	return fmt.Errorf("%q is missing from the transcript", command)
}

func TestWorktreeCommandFeature(t *testing.T) {
	if !gitws.GitAvailable() {
		t.Skip("git binary not available")
	}
	suite := godog.TestSuite{
		Name: "worktree_command",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &worktreeCommandState{}
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				s.close()
				return ctx, nil
			})
			sc.Step(`^a coddy server with a chat in a repository with a bare origin$`, s.serverWithChatInRepository)
			sc.Step(`^the operator sends "([^"]*)"$`, s.sends)
			sc.Step(`^the chat works in the worktree for "([^"]*)"$`, s.worksInWorktree)
			sc.Step(`^the reply names the worktree for "([^"]*)"$`, s.replyNamesWorktree)
			sc.Step(`^the chat works in the main checkout$`, s.worksInMainCheckout)
			sc.Step(`^the "([^"]*)" command is part of the transcript$`, s.commandInTranscript)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/worktree_command.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("worktree_command feature failed")
	}
}
