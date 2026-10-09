package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	apptools "github.com/EvilFreelancer/coddy-agent/internal/tools"
)

type worktreeCreateState struct {
	root      string
	state     *session.State
	manager   *session.Manager
	env       *apptools.Env
	output    string
	worktree  string
	createErr error
	// created is what worktree_create answered.
	created string
}

func (s *worktreeCreateState) git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *worktreeCreateState) setup() error {
	var err error
	s.root, err = os.MkdirTemp("", "coddy-worktree-feature-*")
	if err != nil {
		return err
	}
	if _, err = s.git(s.root, "init", "-b", "main"); err != nil {
		return err
	}
	if _, err = s.git(s.root, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "--allow-empty", "-m", "initial"); err != nil {
		return err
	}
	origin := filepath.Join(s.root, "..", filepath.Base(s.root)+"-origin.git")
	if _, err = s.git(s.root, "clone", "--bare", s.root, origin); err != nil {
		return err
	}
	if _, err = s.git(s.root, "remote", "add", "origin", origin); err != nil {
		return err
	}
	if _, err = s.git(s.root, "fetch", "origin"); err != nil {
		return err
	}
	if _, err = s.git(s.root, "remote", "set-head", "origin", "-a"); err != nil {
		return err
	}
	s.state = &session.State{ID: "sess_worktree_bdd", CWD: s.root, Mode: session.ModeAgent}
	cfg := &config.Config{Paths: config.Paths{Home: s.root, CWD: s.root}}
	s.manager = session.NewManager(cfg, &scriptedQuestionSender{}, nil, slog.Default(), s.root, nil)
	s.env = &apptools.Env{CWD: s.root, SessionID: s.state.ID}
	s.env.SwitchWorkspace = func(ctx context.Context, dir string) error {
		return s.manager.SetSessionWorkspaceDuringTurn(ctx, s.state, dir)
	}
	return nil
}

func (s *worktreeCreateState) create(branch string) error {
	out, err := apptools.NewRegistry().Execute(context.Background(), apptools.ToolWorktreeCreate,
		fmt.Sprintf(`{"branch":%q}`, branch), s.env)
	if err != nil {
		return err
	}
	s.created = out
	s.worktree = s.state.GetCWD()
	return nil
}

// remoteOnlyBranch leaves branch on origin and in refs/remotes/origin, with
// no local branch of that name.
func (s *worktreeCreateState) remoteOnlyBranch(branch string) error {
	for _, args := range [][]string{
		{"branch", branch},
		{"push", "origin", branch},
		{"branch", "-D", branch},
		{"fetch", "origin"},
	} {
		if _, err := s.git(s.root, args...); err != nil {
			return err
		}
	}
	return nil
}

func (s *worktreeCreateState) reportsBranch(branch string) error {
	var answer struct {
		Branch string `json:"branch"`
	}
	if err := json.Unmarshal([]byte(s.created), &answer); err != nil {
		return fmt.Errorf("tool answer %q: %w", s.created, err)
	}
	if answer.Branch != branch {
		return fmt.Errorf("tool reports branch %q, want %q", answer.Branch, branch)
	}
	return nil
}

func (s *worktreeCreateState) worktreeTracks(upstream string) error {
	got, err := s.git(s.worktree, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return err
	}
	if got != upstream {
		return fmt.Errorf("worktree upstream = %q, want %q", got, upstream)
	}
	return nil
}

func (s *worktreeCreateState) run(command string) error {
	var err error
	s.output, err = apptools.NewRegistry().Execute(context.Background(), "run_command", fmt.Sprintf(`{"command":%q}`, command), s.env)
	return err
}

func (s *worktreeCreateState) tryCreate(branch string) error {
	_, s.createErr = apptools.NewRegistry().Execute(context.Background(), apptools.ToolWorktreeCreate,
		fmt.Sprintf(`{"branch":%q}`, branch), s.env)
	return nil
}

func (s *worktreeCreateState) refused() error {
	if s.createErr == nil {
		return fmt.Errorf("worktree_create succeeded where a refusal was expected")
	}
	if cwd := s.state.GetCWD(); cwd != s.root {
		return fmt.Errorf("session moved to %q on a refused request", cwd)
	}
	return nil
}

func (s *worktreeCreateState) check() error {
	if s.worktree == s.root || !strings.Contains(s.worktree, filepath.Join(".coddy", "worktrees")) {
		return fmt.Errorf("session did not move into a worktree: %q", s.worktree)
	}
	if !strings.Contains(s.output, s.worktree) {
		return fmt.Errorf("run_command output %q does not name worktree %q", s.output, s.worktree)
	}
	return nil
}

func TestWorktreeCreateFeature(t *testing.T) {
	s := &worktreeCreateState{}
	suite := godog.TestSuite{
		Name: "worktree-create",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) { return ctx, s.setup() })
			sc.After(func(_ context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				if s.root != "" {
					_ = os.RemoveAll(s.root)
					_ = os.RemoveAll(s.root + "-origin.git")
				}
				return context.Background(), nil
			})
			sc.Step(`^a session in a repository with a bare origin$`, func() error { return nil })
			sc.Step(`^the agent creates a worktree for "([^"]*)"$`, s.create)
			sc.Step(`^the agent tries to create a worktree for "([^"]*)"$`, s.tryCreate)
			sc.Step(`^the creation is refused and the session stays in the main checkout$`, s.refused)
			sc.Step(`^the agent runs "([^"]*)" without a cwd argument$`, s.run)
			sc.Step(`^the command runs inside the new worktree$`, s.check)
			sc.Step(`^origin has a branch "([^"]*)" the repository has fetched but not checked out$`, s.remoteOnlyBranch)
			sc.Step(`^the tool reports the branch "([^"]*)"$`, s.reportsBranch)
			sc.Step(`^the new worktree's branch tracks "([^"]*)"$`, s.worktreeTracks)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/worktree_create.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("worktree create feature failed")
	}
}
