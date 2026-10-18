package tools_test

// Godog harness for features/worktree_navigation.feature: worktree_exit and
// worktree_create's base over a real repository with a bare origin, the
// session moved through the manager's workspace switch exactly as a turn
// moves it (the worktree_create harness's setup).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
	apptools "github.com/EvilFreelancer/coddy-agent/internal/tools"
)

type worktreeNavigationState struct {
	worktreeCreateState
	// worktrees maps a branch to the worktree the session opened for it, and
	// tips a branch to the commit it was left on.
	worktrees map[string]string
	tips      map[string]string
}

func (s *worktreeNavigationState) reset() error {
	s.worktrees = map[string]string{}
	s.tips = map[string]string{}
	return s.setup()
}

func (s *worktreeNavigationState) execute(tool, args string) error {
	_, err := apptools.NewRegistry().Execute(context.Background(), tool, args, s.env)
	if err != nil {
		return fmt.Errorf("%s %s: %w", tool, args, err)
	}
	return nil
}

func (s *worktreeNavigationState) worksInWorktree(branch string) error {
	if err := s.execute(apptools.ToolWorktreeCreate, fmt.Sprintf(`{"branch":%q}`, branch)); err != nil {
		return err
	}
	s.worktrees[branch] = s.state.GetCWD()
	return nil
}

func (s *worktreeNavigationState) worksInWorktreeWithCommit(branch string) error {
	if err := s.worksInWorktree(branch); err != nil {
		return err
	}
	dir := s.worktrees[branch]
	if err := os.WriteFile(filepath.Join(dir, "part.txt"), []byte(branch+"\n"), 0o644); err != nil {
		return err
	}
	if _, err := s.git(dir, "add", "part.txt"); err != nil {
		return err
	}
	if _, err := s.git(dir, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-q", "-m", "work on "+branch); err != nil {
		return err
	}
	tip, err := s.git(dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	s.tips[branch] = tip
	return nil
}

func (s *worktreeNavigationState) leaves() error {
	return s.execute(apptools.ToolWorktreeExit, `{}`)
}

func (s *worktreeNavigationState) leavesAndRemoves() error {
	return s.execute(apptools.ToolWorktreeExit, `{"action":"remove"}`)
}

func (s *worktreeNavigationState) createsFromCurrentCommit(branch string) error {
	if err := s.execute(apptools.ToolWorktreeCreate, fmt.Sprintf(`{"branch":%q,"base":"head"}`, branch)); err != nil {
		return err
	}
	s.worktrees[branch] = s.state.GetCWD()
	return nil
}

func (s *worktreeNavigationState) inMainCheckout() error {
	if cwd := s.state.GetCWD(); !session.SameWorkspacePath(cwd, s.root) {
		return fmt.Errorf("session works in %q, want the main checkout %q", cwd, s.root)
	}
	if !session.SameWorkspacePath(s.env.CWD, s.root) {
		return fmt.Errorf("the next tool call would run in %q, want the main checkout %q", s.env.CWD, s.root)
	}
	return nil
}

func (s *worktreeNavigationState) inWorktree(branch string) error {
	want := s.worktrees[branch]
	if cwd := s.state.GetCWD(); want == "" || !session.SameWorkspacePath(cwd, want) {
		return fmt.Errorf("session works in %q, want the worktree for %s (%q)", cwd, branch, want)
	}
	return nil
}

func (s *worktreeNavigationState) worktreeStillThere(branch string) error {
	if _, err := os.Stat(s.worktrees[branch]); err != nil {
		return fmt.Errorf("worktree for %s: %w", branch, err)
	}
	return nil
}

func (s *worktreeNavigationState) worktreeGone(branch string) error {
	path := s.worktrees[branch]
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return fmt.Errorf("worktree for %s still at %s (%v)", branch, path, err)
	}
	return nil
}

func (s *worktreeNavigationState) branchGone(branch string) error {
	out, err := s.git(s.root, "branch", "--list", branch)
	if err != nil {
		return err
	}
	if out != "" {
		return fmt.Errorf("branch %s is still there", branch)
	}
	return nil
}

func (s *worktreeNavigationState) startsAt(branch, from string) error {
	got, err := s.git(s.root, "rev-parse", branch)
	if err != nil {
		return err
	}
	if got != s.tips[from] {
		return fmt.Errorf("%s starts at %s, want %s's commit %s", branch, got, from, s.tips[from])
	}
	return nil
}

func TestWorktreeNavigationFeature(t *testing.T) {
	s := &worktreeNavigationState{}
	suite := godog.TestSuite{
		Name: "worktree-navigation",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) { return ctx, s.reset() })
			sc.After(func(_ context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				if s.root != "" {
					_ = os.RemoveAll(s.root)
					_ = os.RemoveAll(s.root + "-origin.git")
				}
				return context.Background(), nil
			})
			sc.Step(`^a session in a repository with a bare origin$`, func() error { return nil })
			sc.Step(`^the agent works in a worktree for "([^"]*)"$`, s.worksInWorktree)
			sc.Step(`^the agent works in a worktree for "([^"]*)" with a commit of its own$`, s.worksInWorktreeWithCommit)
			sc.Step(`^the agent leaves the worktree$`, s.leaves)
			sc.Step(`^the agent leaves the worktree and removes it$`, s.leavesAndRemoves)
			sc.Step(`^the agent creates a worktree for "([^"]*)" from the current commit$`, s.createsFromCurrentCommit)
			sc.Step(`^the session works in the main checkout again$`, s.inMainCheckout)
			sc.Step(`^the session works in the worktree for "([^"]*)"$`, s.inWorktree)
			sc.Step(`^the worktree for "([^"]*)" is still there$`, s.worktreeStillThere)
			sc.Step(`^the worktree for "([^"]*)" is gone$`, s.worktreeGone)
			sc.Step(`^the branch "([^"]*)" is gone$`, s.branchGone)
			sc.Step(`^the branch "([^"]*)" starts at the commit of "([^"]*)"$`, s.startsAt)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/worktree_navigation.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("worktree navigation feature failed")
	}
}
