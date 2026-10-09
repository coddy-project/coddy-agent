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
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type docsHTTPState struct {
	root string
	ts   *httptest.Server
	srv  *Server
	body map[string]interface{}

	// What the last turn ran with: its language and the documentation
	// attachments of its prompt.
	mu       sync.Mutex
	turnLang string
	attached []string
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
	s.root, s.ts, s.srv, s.body = "", nil, nil, nil
	s.mu.Lock()
	s.turnLang, s.attached = "", nil
	s.mu.Unlock()
}

func (s *docsHTTPState) runningServe() error {
	root, err := os.MkdirTemp("", "coddy-bdd-docs-http-*")
	if err != nil {
		return err
	}
	s.root = root
	cfg := &config.Config{
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	cfg.Paths.Home = filepath.Join(root, "home")
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		s.mu.Lock()
		s.turnLang = st.GetTurnLang()
		s.attached = nil
		for _, b := range prompt {
			if b.Resource != nil && strings.HasPrefix(b.Resource.URI, "coddy:") {
				s.attached = append(s.attached, b.Resource.Text)
			}
		}
		s.mu.Unlock()
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "stub"})
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

func (s *docsHTTPState) sendsTurnWithLanguage(text, lang string) error {
	body := fmt.Sprintf(`{"model":"agent","input":%q,"stream":false,"metadata":{"surface":"webui","lang":%q}}`, text, lang)
	resp, err := http.Post(s.ts.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /v1/responses: %d", resp.StatusCode)
	}
	return nil
}

func (s *docsHTTPState) turnRanInRussianWith(heading string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnLang != "ru" {
		return fmt.Errorf("the turn ran in %q", s.turnLang)
	}
	if len(s.attached) != 1 || !strings.HasPrefix(s.attached[0], "## "+heading+"\n") {
		return fmt.Errorf("the prompt does not carry the Russian section %q: %q", heading, s.attached)
	}
	return nil
}

func (s *docsHTTPState) asksContentsInRussian() error {
	if err := s.get("/coddy/docs?lang=ru"); err != nil {
		return err
	}
	if s.body["lang"] != "ru" {
		return fmt.Errorf("the contents are not said to be Russian: lang=%v", s.body["lang"])
	}
	return nil
}

func (s *docsHTTPState) opensPageInRussian(ref string) error {
	return s.get("/coddy/docs/page?lang=ru&ref=" + url.QueryEscape(ref))
}

func (s *docsHTTPState) getsSectionInRussian(heading, anchor string) error {
	md, _ := s.body["markdown"].(string)
	if s.body["lang"] != "ru" || s.body["anchor"] != anchor || !strings.Contains(md, "\n## "+heading+"\n") {
		return fmt.Errorf("want the Russian page with the section %q at #%s: lang=%v anchor=%v", heading, anchor, s.body["lang"], s.body["anchor"])
	}
	headings, _ := s.body["headings"].([]interface{})
	for _, h := range headings {
		hm, _ := h.(map[string]interface{})
		if hm["text"] == heading && hm["anchor"] == anchor {
			if u, _ := s.body["url"].(string); !strings.HasPrefix(u, "https://coddy.dev/ru/docs/") {
				return fmt.Errorf("the public address is not the Russian one: %v", s.body["url"])
			}
			return nil
		}
	}
	return fmt.Errorf("no heading %q with the anchor %q: %v", heading, anchor, headings)
}

func (s *docsHTTPState) searchesInRussian(q string) error {
	return s.get("/coddy/docs/search?lang=ru&q=" + url.QueryEscape(q))
}

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
	sc.Step(`^the web UI sends "([^"]*)" with its language "([^"]*)"$`, s.sendsTurnWithLanguage)
	sc.Step(`^the turn runs in Russian with the section "([^"]*)" attached$`, s.turnRanInRussianWith)
	sc.Step(`^the browser asks for the documentation contents in Russian$`, s.asksContentsInRussian)
	sc.Step(`^the browser opens the page "([^"]*)" in Russian$`, s.opensPageInRussian)
	sc.Step(`^it gets the section "([^"]*)" in Russian under the anchor "([^"]*)"$`, s.getsSectionInRussian)
	sc.Step(`^the browser searches the documentation for "([^"]*)" in Russian$`, s.searchesInRussian)
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
