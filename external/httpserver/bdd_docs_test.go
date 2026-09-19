//go:build http

package httpserver

// Godog harness for the @http scenario of features/builtin_docs.feature: a
// real httptest server over a session manager with a stub runner, asked for
// the contents, a page and a search the way the web UI's reader asks.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type docsHTTPState struct {
	root string
	ts   *httptest.Server
	srv  *Server
	body map[string]interface{}
}

func (s *docsHTTPState) close() {
	if s.ts != nil {
		s.ts.Close()
	}
	if s.srv != nil {
		s.srv.Drain()
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
	}
	*s = docsHTTPState{}
}

func (s *docsHTTPState) runningServe() error {
	root, err := os.MkdirTemp("", "coddy-bdd-docs-http-*")
	if err != nil {
		return err
	}
	s.root = root
	cfg := &config.Config{}
	cfg.Paths.Home = filepath.Join(root, "home")
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, nil)
	s.srv = New(cfg, mgr, slog.Default(), root)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *docsHTTPState) get(path string) error {
	resp, err := http.Get(s.ts.URL + path)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	s.body = nil
	if err := json.NewDecoder(resp.Body).Decode(&s.body); err != nil {
		return fmt.Errorf("GET %s: %d, not JSON: %v", path, resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %d %v", path, resp.StatusCode, s.body)
	}
	return nil
}

func (s *docsHTTPState) asksContents() error { return s.get("/coddy/docs") }

func (s *docsHTTPState) contentsListGroup(group, slug, title string) error {
	groups, _ := s.body["groups"].([]interface{})
	for _, g := range groups {
		gm, _ := g.(map[string]interface{})
		if gm["title"] != group {
			continue
		}
		pages, _ := gm["pages"].([]interface{})
		for _, p := range pages {
			pm, _ := p.(map[string]interface{})
			if pm["slug"] == slug && pm["title"] == title && pm["summary"] != "" {
				return nil
			}
		}
		return fmt.Errorf("group %q has no page %s titled %q: %v", group, slug, title, pages)
	}
	return fmt.Errorf("no group %q: %v", group, s.body)
}

func (s *docsHTTPState) opensPage(ref string) error {
	return s.get("/coddy/docs/page?ref=" + url.QueryEscape(ref))
}

func (s *docsHTTPState) getsMarkdownWithNeighbours(title string) error {
	md, _ := s.body["markdown"].(string)
	if s.body["title"] != title || !strings.HasPrefix(md, "# "+title) {
		return fmt.Errorf("want the page %q: %v", title, s.body["title"])
	}
	headings, _ := s.body["headings"].([]interface{})
	if len(headings) < 3 {
		return fmt.Errorf("the page lists %d headings", len(headings))
	}
	for _, key := range []string{"prev", "next"} {
		n, _ := s.body[key].(map[string]interface{})
		if n == nil || n["slug"] == "" || n["title"] == "" {
			return fmt.Errorf("no %s page: %v", key, s.body[key])
		}
	}
	return nil
}

func (s *docsHTTPState) searches(q string) error {
	return s.get("/coddy/docs/search?q=" + url.QueryEscape(q))
}

func (s *docsHTTPState) resultsInclude(slug string) error {
	hits, _ := s.body["hits"].([]interface{})
	for _, h := range hits {
		hm, _ := h.(map[string]interface{})
		if hm["slug"] == slug {
			if snip, _ := hm["snippet"].([]interface{}); len(snip) == 0 {
				return fmt.Errorf("hit %s has no snippet", slug)
			}
			return nil
		}
	}
	return fmt.Errorf("no hit on %s: %v", slug, hits)
}

func initializeDocsHTTPScenario(sc *godog.ScenarioContext) {
	s := &docsHTTPState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a running coddy serve$`, s.runningServe)
	sc.Step(`^the browser asks for the documentation contents$`, s.asksContents)
	sc.Step(`^the contents list the group "([^"]*)" with the page "([^"]*)" titled "([^"]*)"$`, s.contentsListGroup)
	sc.Step(`^the browser opens the page "([^"]*)"$`, s.opensPage)
	sc.Step(`^it gets the Markdown of "([^"]*)" with its sections, the page before it and the page after it$`, s.getsMarkdownWithNeighbours)
	sc.Step(`^the browser searches the documentation for "([^"]*)"$`, s.searches)
	sc.Step(`^the results include the page "([^"]*)"$`, s.resultsInclude)
}

func TestBuiltinDocsHTTPFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "builtin-docs-http",
		ScenarioInitializer: initializeDocsHTTPScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/builtin_docs.feature"},
			Tags:     "@http",
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("builtin docs http feature suite failed")
	}
}
