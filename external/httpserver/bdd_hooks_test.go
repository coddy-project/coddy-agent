//go:build http

package httpserver

// Godog harness for the @http scenario of features/hooks_project_trust.feature:
// a real httptest server over a real session.Manager with a stub runner, a
// workspace carrying a project-scope hooks file, and the catalog and approval
// routes exercised through HTTP.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type hooksHTTPState struct {
	root, home, cwd string
	ts              *httptest.Server
	srv             *Server
	status          int
	body            map[string]interface{}
	prevHOME        string
}

func (s *hooksHTTPState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-hooks-http-*")
	if err != nil {
		return err
	}
	s.root = root
	s.status = 0
	s.body = nil
	return nil
}

func (s *hooksHTTPState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.prevHOME != "" {
		_ = os.Setenv("CODDY_HOME", s.prevHOME)
	} else if s.root != "" {
		_ = os.Unsetenv("CODDY_HOME")
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *hooksHTTPState) startServer() error {
	s.home = filepath.Join(s.root, "home")
	s.cwd = filepath.Join(s.root, "workspace")
	for _, dir := range []string{s.home, s.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	s.prevHOME = os.Getenv("CODDY_HOME")
	if err := os.Setenv("CODDY_HOME", s.home); err != nil {
		return err
	}
	cfgPath := filepath.Join(s.home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("skills:\n  sources: []\n"), 0o644); err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.cwd, nil)
	s.srv = New(cfg, mgr, slog.Default(), s.cwd)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *hooksHTTPState) projectHook(tool string) error {
	dir := filepath.Join(s.cwd, ".coddy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf(`{"hooks":{"PreToolUse":[{"matcher":%q,"hooks":[{"type":"command","command":"./guard.sh"}]}]}}`, tool)
	return os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(body), 0o600)
}

func (s *hooksHTTPState) do(method, path string, body interface{}) error {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, s.ts.URL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	s.status = res.StatusCode
	s.body = nil
	data, _ := io.ReadAll(res.Body)
	if len(data) > 0 {
		_ = json.Unmarshal(data, &s.body)
	}
	return nil
}

func (s *hooksHTTPState) listHooks() error {
	if err := s.do(http.MethodGet, "/coddy/hooks?cwd="+s.cwd, nil); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("GET /coddy/hooks: status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *hooksHTTPState) entry(file string) (map[string]interface{}, error) {
	items, _ := s.body["items"].([]interface{})
	for _, raw := range items {
		e, _ := raw.(map[string]interface{})
		if e["file"] == file {
			return e, nil
		}
	}
	return nil, fmt.Errorf("no entry %q in %v", file, s.body)
}

func (s *hooksHTTPState) listShowsAwaitingWithHook(file, tool string) error {
	e, err := s.entry(file)
	if err != nil {
		return err
	}
	if e["needs_approval"] != true || e["trust"] != "needs_approval" {
		return fmt.Errorf("entry %q should await approval: %v", file, e)
	}
	hooks, _ := e["hooks"].([]interface{})
	if len(hooks) != 1 {
		return fmt.Errorf("entry %q should list one hook: %v", file, e)
	}
	h, _ := hooks[0].(map[string]interface{})
	if h["event"] != "PreToolUse" || h["matcher"] != tool {
		return fmt.Errorf("hook row = %v", h)
	}
	return nil
}

func (s *hooksHTTPState) listShowsTrusted(file string) error {
	e, err := s.entry(file)
	if err != nil {
		return err
	}
	if e["trusted"] != true || e["trust"] != "trusted" {
		return fmt.Errorf("entry %q should be trusted: %v", file, e)
	}
	return nil
}

func (s *hooksHTTPState) listShowsAwaiting(file string) error {
	e, err := s.entry(file)
	if err != nil {
		return err
	}
	if e["needs_approval"] != true {
		return fmt.Errorf("entry %q should await approval: %v", file, e)
	}
	return nil
}

func (s *hooksHTTPState) approve(file string) error {
	if err := s.do(http.MethodPost, "/coddy/hooks/trust", map[string]string{"cwd": s.cwd, "file": file}); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("POST /coddy/hooks/trust: status %d body %v", s.status, s.body)
	}
	item, _ := s.body["item"].(map[string]interface{})
	if item["trusted"] != true {
		return fmt.Errorf("trust response should carry the refreshed entry: %v", s.body)
	}
	return nil
}

func (s *hooksHTTPState) revoke(file string) error {
	if err := s.do(http.MethodPost, "/coddy/hooks/untrust", map[string]string{"cwd": s.cwd, "file": file}); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("POST /coddy/hooks/untrust: status %d body %v", s.status, s.body)
	}
	return nil
}

func initializeHooksHTTPScenario(sc *godog.ScenarioContext) {
	s := &hooksHTTPState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a running coddy HTTP server$`, s.startServer)
	sc.Step(`^the workspace's \.coddy/hooks\.json has a PreToolUse hook for "([^"]*)"$`, s.projectHook)
	sc.Step(`^I list the hooks$`, s.listHooks)
	sc.Step(`^the hooks list shows "([^"]*)" as awaiting approval with a PreToolUse hook for "([^"]*)"$`, s.listShowsAwaitingWithHook)
	sc.Step(`^the hooks list shows "([^"]*)" as trusted$`, s.listShowsTrusted)
	sc.Step(`^the hooks list shows "([^"]*)" as awaiting approval$`, s.listShowsAwaiting)
	sc.Step(`^I approve the hook file "([^"]*)"$`, s.approve)
	sc.Step(`^I revoke the hook file "([^"]*)"$`, s.revoke)
}

func TestHooksTrustHTTPFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "hooks-http",
		ScenarioInitializer: initializeHooksHTTPScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/hooks_project_trust.feature"},
			Tags:     "@http",
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("hooks HTTP feature suite failed")
	}
}
