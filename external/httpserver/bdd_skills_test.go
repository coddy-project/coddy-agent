//go:build http

package httpserver

// Godog harness for features/skills_marketplace.feature: drives the live HTTP
// surface for remote skill install, version tracking, update detection, and
// marketplace source management against a local git marketplace fixture.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type skFeatureState struct {
	root     string
	home     string
	ts       *httptest.Server
	mgr      *session.Manager
	srv      *Server
	markets  map[string]string // marketplace name -> repo path
	updates  map[string]map[string]interface{}
	sources  []string
	status   int
	body     map[string]interface{}
	prevHOME string
}

func (s *skFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-skills-*")
	if err != nil {
		return err
	}
	s.root = root
	s.markets = map[string]string{}
	s.updates = nil
	s.sources = nil
	s.status = 0
	s.body = nil
	return nil
}

func (s *skFeatureState) close() {
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
	} else {
		_ = os.Unsetenv("CODDY_HOME")
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *skFeatureState) startServer() error {
	s.home = filepath.Join(s.root, "home")
	if err := os.MkdirAll(filepath.Join(s.home, "memory"), 0o755); err != nil {
		return err
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
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.home, nil)
	s.srv = New(cfg, s.mgr, slog.Default(), s.home)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

// ---- HTTP helpers ----

func (s *skFeatureState) do(method, path string, body interface{}) error {
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
	raw, _ := io.ReadAll(res.Body)
	s.body = map[string]interface{}{}
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &s.body)
	}
	return nil
}

// ---- marketplace fixture ----

func (s *skFeatureState) marketplaceURL(name string) string {
	return "file://" + s.markets[name]
}

func writeMarketplace(repo, market, skill, version string) error {
	mfDir := filepath.Join(repo, ".claude-plugin")
	if err := os.MkdirAll(mfDir, 0o755); err != nil {
		return err
	}
	manifest := map[string]interface{}{
		"name":     market,
		"metadata": map[string]interface{}{"version": version},
		"plugins": []map[string]interface{}{{
			"name":        skill,
			"source":      "./skills/" + skill,
			"description": "Demo skill " + skill,
			"version":     version,
			"category":    "workflow",
			"license":     "MIT",
		}},
	}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(mfDir, "marketplace.json"), data, 0o644); err != nil {
		return err
	}
	skillDir := filepath.Join(repo, "skills", skill)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return err
	}
	skillMD := fmt.Sprintf("---\nname: %s\ndescription: Demo skill %s\nversion: %s\n---\n\n# %s\n\nDemo body.\n", skill, skill, version, skill)
	return os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644)
}

func (s *skFeatureState) publishMarketplace(market, skill, version string) error {
	repo, ok := s.markets[market]
	fresh := !ok
	if !ok {
		repo = filepath.Join(s.root, "markets", market)
		if err := os.MkdirAll(repo, 0o755); err != nil {
			return err
		}
		s.markets[market] = repo
	}
	if err := writeMarketplace(repo, market, skill, version); err != nil {
		return err
	}
	if fresh {
		if err := bddGit(repo, "init", "-q"); err != nil {
			return err
		}
		if err := bddGit(repo, "config", "user.email", "t@t.io"); err != nil {
			return err
		}
		if err := bddGit(repo, "config", "user.name", "t"); err != nil {
			return err
		}
	}
	if err := bddGit(repo, "add", "-A"); err != nil {
		return err
	}
	return bddGit(repo, "commit", "-q", "-m", "publish "+skill+" "+version)
}

// ---- steps ----

func (s *skFeatureState) givenMarketplace(market, skill, version string) error {
	return s.publishMarketplace(market, skill, version)
}

func (s *skFeatureState) republishMarketplace(market, skill, version string) error {
	return s.publishMarketplace(market, skill, version)
}

func (s *skFeatureState) addSourceAndSync(market string) error {
	if err := s.do(http.MethodPost, "/coddy/skills/sources", map[string]interface{}{
		"source": s.marketplaceURL(market),
		"sync":   true,
	}); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("add+sync status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *skFeatureState) checkUpdates() error {
	if err := s.do(http.MethodGet, "/coddy/skills/updates", nil); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("check updates status %d body %v", s.status, s.body)
	}
	s.updates = map[string]map[string]interface{}{}
	items, _ := s.body["items"].([]interface{})
	for _, it := range items {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		s.updates[name] = m
	}
	return nil
}

func (s *skFeatureState) updateSkill(name string) error {
	if err := s.do(http.MethodPost, "/coddy/skills/"+url.PathEscape(name)+"/update", nil); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("update skill status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *skFeatureState) listSources() error {
	if err := s.do(http.MethodGet, "/coddy/skills/sources", nil); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("list sources status %d body %v", s.status, s.body)
	}
	s.sources = nil
	items, _ := s.body["items"].([]interface{})
	for _, it := range items {
		if str, ok := it.(string); ok {
			s.sources = append(s.sources, str)
		}
	}
	return nil
}

func (s *skFeatureState) removeSource(market string) error {
	q := "?source=" + url.QueryEscape(s.marketplaceURL(market))
	if err := s.do(http.MethodDelete, "/coddy/skills/sources"+q, nil); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("remove source status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *skFeatureState) skillRow(name string) (map[string]interface{}, error) {
	if err := s.do(http.MethodGet, "/coddy/skills", nil); err != nil {
		return nil, err
	}
	items, _ := s.body["items"].([]interface{})
	for _, it := range items {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		if n, _ := m["name"].(string); n == name {
			return m, nil
		}
	}
	return nil, fmt.Errorf("skill %q not in list %v", name, s.body["items"])
}

func (s *skFeatureState) listShowsVersion(name, version string) error {
	row, err := s.skillRow(name)
	if err != nil {
		return err
	}
	if got, _ := row["version"].(string); got != version {
		return fmt.Errorf("skill %q version = %q, want %q", name, got, version)
	}
	return nil
}

func (s *skFeatureState) reportsNoUpdate(name string) error {
	if err := s.checkUpdates(); err != nil {
		return err
	}
	if m, ok := s.updates[name]; ok {
		if avail, _ := m["update_available"].(bool); avail {
			return fmt.Errorf("skill %q unexpectedly reports update available: %v", name, m)
		}
	}
	return nil
}

func (s *skFeatureState) reportsUpdate(name, latest string) error {
	m, ok := s.updates[name]
	if !ok {
		return fmt.Errorf("skill %q not in updates %v", name, s.updates)
	}
	if avail, _ := m["update_available"].(bool); !avail {
		return fmt.Errorf("skill %q reports no update, want update to %q: %v", name, latest, m)
	}
	if got, _ := m["latest"].(string); got != latest {
		return fmt.Errorf("skill %q latest = %q, want %q", name, got, latest)
	}
	return nil
}

func (s *skFeatureState) sourceListContains(market string) error {
	if err := s.listSources(); err != nil { // assert against current server state
		return err
	}
	want := s.marketplaceURL(market)
	for _, src := range s.sources {
		if src == want {
			return nil
		}
	}
	return fmt.Errorf("sources %v does not contain %q", s.sources, want)
}

func (s *skFeatureState) sourceListEmpty() error {
	if err := s.listSources(); err != nil { // assert against current server state
		return err
	}
	if len(s.sources) != 0 {
		return fmt.Errorf("sources not empty: %v", s.sources)
	}
	return nil
}

func initializeSkillsScenario(sc *godog.ScenarioContext) {
	s := &skFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy HTTP server$`, s.startServer)
	sc.Step(`^a local marketplace "([^"]*)" publishing skill "([^"]*)" at version "([^"]*)"$`, s.givenMarketplace)
	sc.Step(`^I have added and synced the marketplace "([^"]*)"$`, s.addSourceAndSync)
	sc.Step(`^I add the marketplace "([^"]*)" as a skill source and sync$`, s.addSourceAndSync)
	sc.Step(`^the marketplace "([^"]*)" republishes skill "([^"]*)" at version "([^"]*)"$`, s.republishMarketplace)
	sc.Step(`^I check for skill updates$`, s.checkUpdates)
	sc.Step(`^I update the skill "([^"]*)"$`, s.updateSkill)
	sc.Step(`^I list the skill sources$`, s.listSources)
	sc.Step(`^I remove the marketplace "([^"]*)" from the skill sources$`, s.removeSource)

	sc.Step(`^the skills list shows "([^"]*)" at version "([^"]*)"$`, s.listShowsVersion)
	sc.Step(`^the skills list still shows "([^"]*)" at version "([^"]*)"$`, s.listShowsVersion)
	sc.Step(`^skill "([^"]*)" reports no update available$`, s.reportsNoUpdate)
	sc.Step(`^skill "([^"]*)" reports an update available to version "([^"]*)"$`, s.reportsUpdate)
	sc.Step(`^the source list contains the marketplace "([^"]*)"$`, s.sourceListContains)
	sc.Step(`^the source list is empty$`, s.sourceListEmpty)
}

func TestSkillsMarketplaceFeature(t *testing.T) {
	if !gitws.GitAvailable() {
		t.Skip("git binary not available")
	}
	suite := godog.TestSuite{
		Name:                "skills_marketplace",
		ScenarioInitializer: initializeSkillsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/skills_marketplace.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("skills_marketplace feature failed")
	}
}
