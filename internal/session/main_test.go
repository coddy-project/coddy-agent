package session_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// runHomeEnv hands the home TestMain made to the processes that re-execute
// this test binary (the ACP helper, the fake MCP servers), so they neither
// make nor remove one of their own.
const runHomeEnv = "CODDY_TEST_SESSION_RUN_HOME"

// TestMain points CODDY_HOME at an empty directory of the test run's own for
// the whole package (see TestTestsDoNotResolveTheOperatorHome). A test that
// needs a home of its own still sets CODDY_HOME itself. HOME stays the
// operator's on purpose: tests run git in temporary repositories and need its
// identity.
func TestMain(m *testing.M) {
	if home := os.Getenv(runHomeEnv); home != "" {
		// A helper process, or a run nested in one: the home is the parent's.
		_ = os.Setenv("CODDY_HOME", home)
		os.Exit(m.Run())
	}
	home, err := os.MkdirTemp("", "coddy-session-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "test home:", err)
		os.Exit(1)
	}
	_ = os.Setenv(runHomeEnv, home)
	_ = os.Setenv("CODDY_HOME", home)
	code := m.Run()
	if err := os.RemoveAll(home); err != nil {
		fmt.Fprintln(os.Stderr, "test home:", err)
	}
	os.Exit(code)
}

// No test of this package may read the global mcp.json, hooks, skills or .env
// of whoever runs the tests: config.Load resolves Paths.Home from CODDY_HOME
// (or ~/.coddy), and a session manager built over that config dials the
// servers <home>/mcp.json declares (issue #458). The home such a load
// resolves is the directory TestMain made for the run, under the temp dir.
func TestTestsDoNotResolveTheOperatorHome(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("agent:\n  max_turns: 8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	runHome := os.Getenv(runHomeEnv)
	if runHome == "" {
		t.Fatalf("no home of the test run's own; config home = %q", cfg.Paths.Home)
	}
	want, err := filepath.EvalSymlinks(runHome)
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(cfg.Paths.Home)
	if err != nil || home != want || !strings.HasPrefix(home, tmp+string(filepath.Separator)) {
		t.Fatalf("config home = %q (%v), want the run's own %q under %q", cfg.Paths.Home, err, want, tmp)
	}
}
