package agent

// Godog harness for features/accept_edits_scope.feature: which writes the
// accept_edits mode approves by itself. A scripted model, no LLM.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"
)

type acceptEditsScopeState struct {
	t                  *testing.T
	cwd, home, outside string
	mode               string
	prompts            int
}

func (s *acceptEditsScopeState) session(mode string) error {
	root := s.t.TempDir()
	s.cwd = filepath.Join(root, "project")
	s.home = filepath.Join(root, "coddy-home")
	s.outside = filepath.Join(root, "elsewhere")
	for _, d := range []string{s.cwd, s.home, s.outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	s.mode = mode
	return nil
}

func (s *acceptEditsScopeState) writes(name, where string) error {
	dir := map[string]string{
		"in the workspace":      s.cwd,
		"in the agent's home":   s.home,
		"outside the workspace": s.outside,
	}[where]
	s.prompts = runOneWrite(s.t, s.mode, s.cwd, s.home, nil, "write",
		map[string]string{"path": filepath.Join(dir, name), "content": "x"})
	return nil
}

func (s *acceptEditsScopeState) received(n int) error {
	if s.prompts != n {
		return fmt.Errorf("permission requests = %d, want %d", s.prompts, n)
	}
	return nil
}

func TestAcceptEditsScopeFeature(t *testing.T) {
	st := &acceptEditsScopeState{t: t}
	suite := godog.TestSuite{
		Name: "accept-edits-scope",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a session in permission mode "([^"]*)"$`, st.session)
			sc.Step(`^the model writes the file "([^"]*)" (in the workspace|in the agent's home|outside the workspace)$`, st.writes)
			sc.Step(`^the client received (\d+) permission requests?$`, st.received)
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				st.prompts = 0
				return ctx, nil
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/accept_edits_scope.feature"},
			TestingT: t,
			Output:   os.Stdout,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("accept_edits scope feature failed")
	}
}
