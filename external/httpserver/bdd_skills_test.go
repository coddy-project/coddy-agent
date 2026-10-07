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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

// offlineSystemSources keeps a test off the network: the built-in marketplace
// is a real GitHub address, and listing, probing or syncing sources would clone
// it. Tests that are about the built-in marketplace itself live in
// internal/skills.
func offlineSystemSources(t *testing.T) {
	t.Helper()
	prev := skills.SystemSources
	skills.SystemSources = nil
	t.Cleanup(func() { skills.SystemSources = prev })
}

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
	// Only a home this scenario saved is put back: TestMain always sets one,
	// so an empty value means the scenario never changed it.
	if s.prevHOME != "" {
		_ = os.Setenv("CODDY_HOME", s.prevHOME)
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
	if err := os.WriteFile(cfgPath, []byte("skills:\n  auto_discovery: true\n"), 0o644); err != nil {
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
	defer func() { _ = res.Body.Close() }()
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

// projectDeclares writes the server workspace's .coddy/marketplaces.json with
// the marketplace as a source, the way a checkout brings it.
func (s *skFeatureState) projectDeclares(market string) error {
	return config.WriteMarketplacesFile(config.ProjectMarketplacesPath(s.home), config.MarketplacesFile{Sources: []string{s.marketplaceURL(market)}})
}

// listedEntry finds the entry of the last source listing whose source is the
// marketplace's.
func (s *skFeatureState) listedEntry(market string) (map[string]interface{}, error) {
	entries, _ := s.body["entries"].([]interface{})
	for _, it := range entries {
		m, _ := it.(map[string]interface{})
		if src, _ := m["source"].(string); src == s.marketplaceURL(market) {
			return m, nil
		}
	}
	return nil, fmt.Errorf("the listing has no entry for %q: %v", market, s.body["entries"])
}

func (s *skFeatureState) listedAwaitingApproval(market string) error {
	m, err := s.listedEntry(market)
	if err != nil {
		return err
	}
	if m["origin"] != "project" || m["status"] != "needs_approval" || m["trusted"] != false || m["gated"] != true {
		return fmt.Errorf("entry of %q = %v, want a gated project entry awaiting approval", market, m)
	}
	return nil
}

func (s *skFeatureState) approveListed(market string) error {
	m, err := s.listedEntry(market)
	if err != nil {
		return err
	}
	if err := s.do(http.MethodPost, "/coddy/skills/sources/trust", map[string]interface{}{
		"key": s.marketplaceURL(market), "fingerprint": m["fingerprint"],
	}); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("approve status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *skFeatureState) syncSources() error {
	if err := s.do(http.MethodPost, "/coddy/skills/sync", nil); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("sync status %d body %v", s.status, s.body)
	}
	if failed, _ := s.body["failed"].([]interface{}); len(failed) != 0 {
		return fmt.Errorf("sync failed: %v", failed)
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
	sc.Step(`^the workspace's marketplaces\.json declares the marketplace "([^"]*)"$`, s.projectDeclares)
	sc.Step(`^the source list shows the marketplace "([^"]*)" from the project awaiting approval$`, s.listedAwaitingApproval)
	sc.Step(`^I approve the project marketplace "([^"]*)" as it was listed$`, s.approveListed)
	sc.Step(`^I sync the skill sources$`, s.syncSources)

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
	offlineSystemSources(t)
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

// ---- features/skills_session_workspace.feature ----
//
// Regression for coddy-project/coddy-agent#146: skills.dirs entries written
// with ${CWD} follow the workspace of the session that asks, not the directory
// the server process was started from.

type skCWDFeatureState struct {
	root       string
	home       string
	launch     string
	dirEntry   string
	projects   map[string]string
	ts         *httptest.Server
	mgr        *session.Manager
	srv        *Server
	sessionID  string
	status     int
	body       map[string]interface{}
	turnSkills []string
	mu         sync.Mutex
}

func (s *skCWDFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-skills-cwd-*")
	if err != nil {
		return err
	}
	s.root = root
	s.home = filepath.Join(root, "home")
	s.launch = filepath.Join(root, "launch")
	s.projects = map[string]string{}
	s.dirEntry = ""
	s.sessionID = ""
	s.status = 0
	s.body = nil
	s.turnSkills = nil
	return os.MkdirAll(s.launch, 0o755)
}

func (s *skCWDFeatureState) close() {
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

func (s *skCWDFeatureState) skillsConfiguredUnder(entry string) error {
	s.dirEntry = entry
	return nil
}

func (s *skCWDFeatureState) projectWithLocalSkill(project, skill string) error {
	if s.dirEntry == "" {
		return fmt.Errorf("skills directory entry not configured")
	}
	dir := filepath.Join(s.root, "projects", project)
	skillDir := filepath.Join(skills.ExpandConfiguredPath(s.dirEntry, dir, s.home), skill)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: Local skill %s of project %s\n---\n\n# %s\n\nProject body.\n", skill, skill, project, skill)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		return err
	}
	s.projects[project] = dir
	return nil
}

func (s *skCWDFeatureState) startServerOutsideProject() error {
	if err := os.MkdirAll(s.home, 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(s.home, "config.yaml")
	cfgYAML := fmt.Sprintf(`providers:
  - name: local
    type: openai
    api_key: test-key
models:
  - model: local/gpt-4o
    max_tokens: 4096
    temperature: 0.1
agent:
  model: local/gpt-4o
skills:
  dirs:
    - %q
`, s.dirEntry)
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		return err
	}
	// The process is started from a directory that is not the project, the
	// way a user service started from $HOME would be.
	cfg, err := config.LoadWithPaths(config.Paths{Home: s.home, CWD: s.launch, ConfigPath: cfgPath})
	if err != nil {
		return err
	}
	runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		var names []string
		for _, sum := range skills.ListSkills(st.GetSkills()) {
			names = append(names, sum.Name)
		}
		s.mu.Lock()
		s.turnSkills = names
		s.mu.Unlock()
		return string(acp.StopReasonEndTurn), nil
	}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.launch, nil)
	s.srv = New(cfg, s.mgr, slog.Default(), s.launch)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *skCWDFeatureState) do(method, path string, body interface{}, withSession bool) error {
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
	if withSession {
		if s.sessionID == "" {
			return fmt.Errorf("no session anchored")
		}
		req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	s.status = res.StatusCode
	raw, _ := io.ReadAll(res.Body)
	s.body = map[string]interface{}{}
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &s.body)
	}
	return nil
}

// sessionAnchoredOnProject mirrors the SPA draft flow: a fresh session id is
// pointed at the project folder before the first message.
func (s *skCWDFeatureState) sessionAnchoredOnProject(project string) error {
	dir, ok := s.projects[project]
	if !ok {
		return fmt.Errorf("unknown project %q", project)
	}
	s.sessionID = fmt.Sprintf("sess_%x", time.Now().UnixNano())
	if err := s.do(http.MethodPost, "/coddy/sessions/"+s.sessionID+"/workspace", map[string]interface{}{"path": dir}, false); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("anchor workspace status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *skCWDFeatureState) switchSessionWorkspaceToProject(project string) error {
	dir, ok := s.projects[project]
	if !ok {
		return fmt.Errorf("unknown project %q", project)
	}
	if s.sessionID == "" {
		return fmt.Errorf("no session anchored")
	}
	if err := s.do(http.MethodPost, "/coddy/sessions/"+s.sessionID+"/workspace", map[string]interface{}{"path": dir}, false); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("switch workspace status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *skCWDFeatureState) listSlashCommands(withSession bool) error {
	if err := s.do(http.MethodGet, "/coddy/slash-commands?page=1&page_size=200", nil, withSession); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("slash-commands status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *skCWDFeatureState) listSlashCommandsForSession() error { return s.listSlashCommands(true) }

func (s *skCWDFeatureState) listSlashCommandsWithoutSession() error {
	return s.listSlashCommands(false)
}

// pickedFolderQuery is the query the SPA adds to a cwd-scoped listing while a
// new chat has no session yet: the folder picked on the start screen.
func (s *skCWDFeatureState) pickedFolderQuery(project string) (string, error) {
	dir, ok := s.projects[project]
	if !ok {
		return "", fmt.Errorf("unknown project %q", project)
	}
	return "&cwd=" + url.QueryEscape(dir), nil
}

func (s *skCWDFeatureState) listSlashCommandsPicked(project string, withSession bool) error {
	q, err := s.pickedFolderQuery(project)
	if err != nil {
		return err
	}
	if err := s.do(http.MethodGet, "/coddy/slash-commands?page=1&page_size=200"+q, nil, withSession); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("slash-commands status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *skCWDFeatureState) listSlashCommandsWithoutSessionPicked(project string) error {
	return s.listSlashCommandsPicked(project, false)
}

func (s *skCWDFeatureState) listSlashCommandsForSessionPicked(project string) error {
	return s.listSlashCommandsPicked(project, true)
}

func (s *skCWDFeatureState) listSkillsWithoutSessionPicked(project string) error {
	q, err := s.pickedFolderQuery(project)
	if err != nil {
		return err
	}
	if err := s.do(http.MethodGet, "/coddy/skills?"+strings.TrimPrefix(q, "&"), nil, false); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("skills status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *skCWDFeatureState) projectHasLocalSubagent(project, name string) error {
	dir, ok := s.projects[project]
	if !ok {
		return fmt.Errorf("unknown project %q", project)
	}
	agents := filepath.Join(dir, ".coddy", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: Local subagent of project %s\n---\n\nReview the change.\n", name, project)
	return os.WriteFile(filepath.Join(agents, name+".md"), []byte(body), 0o644)
}

func (s *skCWDFeatureState) searchMentionsPicked(query, project string) error {
	q, err := s.pickedFolderQuery(project)
	if err != nil {
		return err
	}
	if err := s.do(http.MethodGet, "/coddy/mentions?q="+url.QueryEscape(query)+q, nil, false); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("mentions status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *skCWDFeatureState) mentionInserts() []string {
	items, _ := s.body["items"].([]interface{})
	out := make([]string, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]interface{}); ok {
			if v, _ := m["insert"].(string); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

func (s *skCWDFeatureState) mentionCandidatesInclude(insert string) error {
	for _, v := range s.mentionInserts() {
		if v == insert {
			return nil
		}
	}
	return fmt.Errorf("mention candidates %v do not include %q", s.mentionInserts(), insert)
}

func (s *skCWDFeatureState) mentionCandidatesExclude(insert string) error {
	for _, v := range s.mentionInserts() {
		if v == insert {
			return fmt.Errorf("mention candidates %v unexpectedly include %q", s.mentionInserts(), insert)
		}
	}
	return nil
}

// projectLinksSkillFromOutside puts a skill folder outside every project and
// links it into the project's skills directory, the way an operator shares one
// skill between projects.
func (s *skCWDFeatureState) projectLinksSkillFromOutside(project, skill string) error {
	if s.dirEntry == "" {
		return fmt.Errorf("skills directory entry not configured")
	}
	dir, ok := s.projects[project]
	if !ok {
		return fmt.Errorf("unknown project %q", project)
	}
	target := filepath.Join(s.root, "outside", skill)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: Shared skill %s\n---\n\n# %s\n\nShared body.\n", skill, skill, skill)
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte(body), 0o644); err != nil {
		return err
	}
	skillsDir := skills.ExpandConfiguredPath(s.dirEntry, dir, s.home)
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return err
	}
	return os.Symlink(target, filepath.Join(skillsDir, skill))
}

func (s *skCWDFeatureState) deleteSkillForSession(skill string) error {
	if err := s.do(http.MethodDelete, "/coddy/skills/"+url.PathEscape(skill), nil, true); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("delete status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *skCWDFeatureState) skillLinkGone(skill, project string) error {
	dir, ok := s.projects[project]
	if !ok {
		return fmt.Errorf("unknown project %q", project)
	}
	link := filepath.Join(skills.ExpandConfiguredPath(s.dirEntry, dir, s.home), skill)
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		return fmt.Errorf("skill link %s still there (err %v)", link, err)
	}
	return nil
}

func (s *skCWDFeatureState) linkedSkillStillOnDisk(skill string) error {
	if _, err := os.Stat(filepath.Join(s.root, "outside", skill, "SKILL.md")); err != nil {
		return fmt.Errorf("the linked skill %q went with its link: %v", skill, err)
	}
	return nil
}

func (s *skCWDFeatureState) listSkillsForSession() error {
	if err := s.do(http.MethodGet, "/coddy/skills", nil, true); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("skills status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *skCWDFeatureState) itemNames() []string {
	items, _ := s.body["items"].([]interface{})
	out := make([]string, 0, len(items))
	for _, it := range items {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		if n, _ := m["name"].(string); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func (s *skCWDFeatureState) slashCommandsInclude(name string) error {
	for _, n := range s.itemNames() {
		if n == name {
			return nil
		}
	}
	return fmt.Errorf("slash commands %v do not include %q", s.itemNames(), name)
}

func (s *skCWDFeatureState) slashCommandsExclude(name string) error {
	for _, n := range s.itemNames() {
		if n == name {
			return fmt.Errorf("slash commands %v unexpectedly include %q", s.itemNames(), name)
		}
	}
	return nil
}

func (s *skCWDFeatureState) skillsListIncludesFromProject(name, project string) error {
	dir, ok := s.projects[project]
	if !ok {
		return fmt.Errorf("unknown project %q", project)
	}
	items, _ := s.body["items"].([]interface{})
	for _, it := range items {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		if n, _ := m["name"].(string); n != name {
			continue
		}
		fp, _ := m["file_path"].(string)
		if !strings.HasPrefix(bddNormPath(fp), bddNormPath(dir)) {
			return fmt.Errorf("skill %q file_path %q is not under project %q", name, fp, dir)
		}
		return nil
	}
	return fmt.Errorf("skills list %v does not include %q", s.itemNames(), name)
}

func (s *skCWDFeatureState) promptSession(input string) error {
	payload := map[string]interface{}{"model": "agent", "input": input, "stream": false}
	if err := s.do(http.MethodPost, "/v1/responses", payload, true); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("responses status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *skCWDFeatureState) turnRanWithSkill(name string) error {
	s.mu.Lock()
	loaded := append([]string(nil), s.turnSkills...)
	s.mu.Unlock()
	for _, n := range loaded {
		if n == name {
			return nil
		}
	}
	return fmt.Errorf("turn ran with skills %v, want %q loaded", loaded, name)
}

func (s *skCWDFeatureState) readServerConfiguration() error {
	if err := s.do(http.MethodGet, "/coddy/config", nil, false); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("config status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *skCWDFeatureState) skillsDirectoriesInclude(entry string) error {
	sk, _ := s.body["skills"].(map[string]interface{})
	dirs, _ := sk["dirs"].([]interface{})
	var got []string
	for _, d := range dirs {
		if str, ok := d.(string); ok {
			got = append(got, str)
			if str == entry {
				return nil
			}
		}
	}
	return fmt.Errorf("skills.dirs %v do not include %q", got, entry)
}

func initializeSkillsSessionWorkspaceScenario(sc *godog.ScenarioContext) {
	s := &skCWDFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^skills are configured under "([^"]*)"$`, s.skillsConfiguredUnder)
	sc.Step(`^a project folder "([^"]*)" with a local skill "([^"]*)"$`, s.projectWithLocalSkill)
	sc.Step(`^a running coddy HTTP server started outside the project$`, s.startServerOutsideProject)
	sc.Step(`^a session anchored on the project folder "([^"]*)"$`, s.sessionAnchoredOnProject)
	sc.Step(`^I switch that session workspace to the project folder "([^"]*)"$`, s.switchSessionWorkspaceToProject)
	sc.Step(`^I list slash commands for that session$`, s.listSlashCommandsForSession)
	sc.Step(`^I list slash commands without a session$`, s.listSlashCommandsWithoutSession)
	sc.Step(`^I list slash commands without a session for the picked folder "([^"]*)"$`, s.listSlashCommandsWithoutSessionPicked)
	sc.Step(`^I list slash commands for that session with the picked folder "([^"]*)"$`, s.listSlashCommandsForSessionPicked)
	sc.Step(`^I list skills without a session for the picked folder "([^"]*)"$`, s.listSkillsWithoutSessionPicked)
	sc.Step(`^the project folder "([^"]*)" has a local subagent "([^"]*)"$`, s.projectHasLocalSubagent)
	sc.Step(`^I search the mentions "([^"]*)" without a session for the picked folder "([^"]*)"$`, s.searchMentionsPicked)
	sc.Step(`^the mention candidates include "([^"]*)"$`, s.mentionCandidatesInclude)
	sc.Step(`^the mention candidates do not include "([^"]*)"$`, s.mentionCandidatesExclude)
	sc.Step(`^the project folder "([^"]*)" links the skill "([^"]*)" from outside the project$`, s.projectLinksSkillFromOutside)
	sc.Step(`^I delete the skill "([^"]*)" for that session$`, s.deleteSkillForSession)
	sc.Step(`^the skill link "([^"]*)" is gone from the project folder "([^"]*)"$`, s.skillLinkGone)
	sc.Step(`^the linked skill "([^"]*)" is still on disk outside the project$`, s.linkedSkillStillOnDisk)
	sc.Step(`^I list skills for that session$`, s.listSkillsForSession)
	sc.Step(`^I prompt that session with "([^"]*)"$`, s.promptSession)
	sc.Step(`^I read the server configuration$`, s.readServerConfiguration)
	sc.Step(`^the slash commands include "([^"]*)"$`, s.slashCommandsInclude)
	sc.Step(`^the slash commands do not include "([^"]*)"$`, s.slashCommandsExclude)
	sc.Step(`^the skills list includes "([^"]*)" from the project folder "([^"]*)"$`, s.skillsListIncludesFromProject)
	sc.Step(`^the turn runs with the skill "([^"]*)" loaded$`, s.turnRanWithSkill)
	sc.Step(`^the skills directories include "([^"]*)"$`, s.skillsDirectoriesInclude)
}

// ---- features/skills_default_dirs.feature ----

func (s *skCWDFeatureState) projectFolders(a, b string) error {
	for _, name := range []string{a, b} {
		dir := filepath.Join(s.root, "projects", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		s.projects[name] = dir
	}
	return nil
}

func writeDescribedSkill(dir, name, description string) error {
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n# %s\n", name, description, name)
	return os.WriteFile(filepath.Join(dir, name, "SKILL.md"), []byte(body), 0o644)
}

func (s *skCWDFeatureState) projectHasSkillIn(project, skill, rel, description string) error {
	dir, ok := s.projects[project]
	if !ok {
		return fmt.Errorf("unknown project %q", project)
	}
	return writeDescribedSkill(filepath.Join(dir, filepath.FromSlash(rel)), skill, description)
}

func (s *skCWDFeatureState) coddyHomeHasSkill(skill, description string) error {
	return writeDescribedSkill(filepath.Join(s.home, "skills"), skill, description)
}

func (s *skCWDFeatureState) extraDirHasSkill(extra, skill, description string) error {
	return writeDescribedSkill(filepath.Join(s.root, extra), skill, description)
}

func (s *skCWDFeatureState) projectLinksFolderOutside(project, rel, skill string) error {
	dir, ok := s.projects[project]
	if !ok {
		return fmt.Errorf("unknown project %q", project)
	}
	outside := filepath.Join(s.root, "outside-"+skill)
	if err := writeDescribedSkill(outside, skill, "behind a link"); err != nil {
		return err
	}
	link := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	return os.Symlink(outside, link)
}

// startServerWithSkills starts the server from a directory that is not a
// project, with skillsBlock as the whole skills section of its config ("" for
// none at all).
func (s *skCWDFeatureState) startServerWithSkills(skillsBlock string) error {
	if err := os.MkdirAll(s.home, 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(s.home, "config.yaml")
	cfgYAML := `providers:
  - name: local
    type: openai
    api_key: test-key
models:
  - model: local/gpt-4o
    max_tokens: 4096
    temperature: 0.1
agent:
  model: local/gpt-4o
` + skillsBlock
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		return err
	}
	cfg, err := config.LoadWithPaths(config.Paths{Home: s.home, CWD: s.launch, ConfigPath: cfgPath})
	if err != nil {
		return err
	}
	runner := func(_ context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.launch, nil)
	s.srv = New(cfg, s.mgr, slog.Default(), s.launch)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *skCWDFeatureState) startServerNoSkillDirs() error { return s.startServerWithSkills("") }

func (s *skCWDFeatureState) startServerExtraSkillDir(extra string) error {
	return s.startServerWithSkills(fmt.Sprintf("skills:\n  dirs:\n    - %q\n", filepath.ToSlash(filepath.Join(s.root, extra))))
}

func (s *skCWDFeatureState) slashCommandDescribed(name, description string) error {
	items, _ := s.body["items"].([]interface{})
	for _, it := range items {
		m, ok := it.(map[string]interface{})
		if !ok || m["name"] != name {
			continue
		}
		if got, _ := m["description"].(string); got != description {
			return fmt.Errorf("slash command %q is described %q, want %q", name, got, description)
		}
		return nil
	}
	return fmt.Errorf("slash commands %v do not include %q", s.itemNames(), name)
}

func initializeSkillsDefaultDirsScenario(sc *godog.ScenarioContext) {
	s := &skCWDFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^project folders "([^"]*)" and "([^"]*)"$`, s.projectFolders)
	sc.Step(`^the project folder "([^"]*)" has the skill "([^"]*)" in "([^"]*)" described "([^"]*)"$`, s.projectHasSkillIn)
	sc.Step(`^Coddy's own skills folder has the skill "([^"]*)" described "([^"]*)"$`, s.coddyHomeHasSkill)
	sc.Step(`^the extra directory "([^"]*)" has the skill "([^"]*)" described "([^"]*)"$`, s.extraDirHasSkill)
	sc.Step(`^the project folder "([^"]*)" links "([^"]*)" to a folder outside it with the skill "([^"]*)"$`, s.projectLinksFolderOutside)
	sc.Step(`^a running coddy HTTP server with no skill directories configured$`, s.startServerNoSkillDirs)
	sc.Step(`^a running coddy HTTP server with the extra skill directory "([^"]*)"$`, s.startServerExtraSkillDir)
	sc.Step(`^a session anchored on the project folder "([^"]*)"$`, s.sessionAnchoredOnProject)
	sc.Step(`^I switch that session workspace to the project folder "([^"]*)"$`, s.switchSessionWorkspaceToProject)
	sc.Step(`^I list slash commands for that session$`, s.listSlashCommandsForSession)
	sc.Step(`^I list slash commands without a session for the picked folder "([^"]*)"$`, s.listSlashCommandsWithoutSessionPicked)
	sc.Step(`^the slash commands include "([^"]*)"$`, s.slashCommandsInclude)
	sc.Step(`^the slash commands do not include "([^"]*)"$`, s.slashCommandsExclude)
	sc.Step(`^the slash command "([^"]*)" is described "([^"]*)"$`, s.slashCommandDescribed)
}

func TestSkillsDefaultDirsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "skills_default_dirs",
		ScenarioInitializer: initializeSkillsDefaultDirsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/skills_default_dirs.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("skills_default_dirs feature failed")
	}
}

func TestSkillsSessionWorkspaceFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "skills_session_workspace",
		ScenarioInitializer: initializeSkillsSessionWorkspaceScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/skills_session_workspace.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("skills_session_workspace feature failed")
	}
}
