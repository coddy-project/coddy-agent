package skills

// Godog harness for features/plugin_archive_source.feature and
// features/plugin_marketplace_commands.feature: an https marketplace on
// httptest publishes plugins as zip archives, and the plugin command - the
// dispatcher behind both `coddy plugin` and the chat /plugin - adds the
// marketplace, installs, checks and syncs its plugins in a temporary home that
// has no git on its PATH.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/web"
)

type archiveFeatureState struct {
	root     string
	home     string
	cfg      *config.Config
	srv      *httptest.Server
	answer   string
	restores []func()

	mu      sync.Mutex // guards markets, which the server goroutine reads
	markets map[string]*archiveMarket
}

// archiveMarket is one marketplace the test server publishes.
type archiveMarket struct {
	plugins []*archivePluginPack
}

func (m *archiveMarket) plugin(name string) *archivePluginPack {
	for _, p := range m.plugins {
		if p.name == name {
			return p
		}
	}
	return nil
}

// archivePluginPack is one plugin of a marketplace, whose archive is rebuilt
// at every revision.
type archivePluginPack struct {
	name       string
	wrapped    bool
	declareSHA bool
	authorRepo bool // the author's whole repository, test data with a SKILL.md included
	rootSkill  bool // the plugin is one skill: SKILL.md at its root, no skills/, no "skills" field
	revision   int
	archive    []byte
}

// build packs the plugin the way the catalogue does: .claude-plugin/plugin.json
// and the skill with an executable script, optionally under one wrapping folder,
// or an author's repository as it is, other folders and all.
func (p *archivePluginPack) build() error {
	prefix := ""
	if p.wrapped || p.authorRepo {
		prefix = p.name + "-main/"
	}
	skill := fmt.Sprintf("---\nname: %s\ndescription: the %s skill, published as an archive\n---\n\n# %s\n\nrevision %d\n",
		p.name, p.name, p.name, p.revision)
	if p.rootSkill {
		// Shaped like EvilFreelancer/logika: the manifest names no skills, and
		// the plugin root is the skill, its command and references beside it.
		data, err := zipOf(
			zipEntry{name: ".claude-plugin/plugin.json", body: `{"name":"` + p.name + `","description":"a plugin that is one skill"}`},
			zipEntry{name: "SKILL.md", body: skill},
			zipEntry{name: "scripts/run.sh", body: "#!/bin/sh\necho run\n", mode: 0o755},
			zipEntry{name: "commands/review.md", body: "Review the argument.\n"},
			zipEntry{name: "references/notes.md", body: "notes\n"},
		)
		if err != nil {
			return err
		}
		p.archive = data
		return nil
	}
	entries := []zipEntry{
		{name: prefix + ".claude-plugin/plugin.json", body: `{"name":"` + p.name + `","description":"an archive plugin"}`},
		{name: prefix + "skills/" + p.name + "/SKILL.md", body: skill},
		{name: prefix + "skills/" + p.name + "/scripts/run.sh", body: "#!/bin/sh\necho run\n", mode: 0o755},
	}
	if p.authorRepo {
		entries = append(entries,
			zipEntry{name: prefix + "README.md", body: "# " + p.name + "\n"},
			zipEntry{name: prefix + "commands/check.md", body: "Check the text.\n"},
			zipEntry{name: prefix + "tools/testdata/corpus/SKILL.md", body: "---\nname: corpus\ndescription: a test fixture, not a skill\n---\n"},
		)
	}
	data, err := zipOf(entries...)
	if err != nil {
		return err
	}
	p.archive = data
	return nil
}

func newPluginPack(name, layout string) (*archivePluginPack, error) {
	p := &archivePluginPack{
		name:       name,
		wrapped:    layout == "wrapped in one folder",
		declareSHA: layout == "with its sha256",
		authorRepo: layout == "of its author's repository with a SKILL.md in its test data",
		rootSkill:  layout == "with its skill at the plugin root",
		revision:   1,
	}
	return p, p.build()
}

func (s *archiveFeatureState) reset() {
	s.close()
	s.markets = map[string]*archiveMarket{}
	s.answer = ""
}

func (s *archiveFeatureState) close() {
	if s.srv != nil {
		s.srv.Close()
		s.srv = nil
	}
	for i := len(s.restores) - 1; i >= 0; i-- {
		s.restores[i]()
	}
	s.restores = nil
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
	s.cfg = nil
}

// serve publishes /marketplace/<market>.json and /plugins/<market>/<plugin>.zip.
func (s *archiveFeatureState) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case strings.HasPrefix(r.URL.Path, "/marketplace/"):
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/marketplace/"), ".json")
		m := s.markets[name]
		if m == nil {
			http.NotFound(w, r)
			return
		}
		plugins := make([]any, 0, len(m.plugins))
		for _, p := range m.plugins {
			src := map[string]string{
				"source": "archive",
				"url":    "https://" + r.Host + "/plugins/" + name + "/" + p.name + ".zip",
			}
			if p.declareSHA {
				src["sha256"] = sha256Hex(p.archive)
			}
			plugins = append(plugins, map[string]any{
				"name":        p.name,
				"description": "a plugin published as a zip archive",
				"source":      src,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":    name,
			"owner":   map[string]string{"name": "catalogue"},
			"plugins": plugins,
		})
	case strings.HasPrefix(r.URL.Path, "/plugins/"):
		market, file, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/plugins/"), "/")
		m := s.markets[market]
		if m == nil {
			http.NotFound(w, r)
			return
		}
		p := m.plugin(strings.TrimSuffix(file, ".zip"))
		if p == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(p.archive)
	default:
		http.NotFound(w, r)
	}
}

func (s *archiveFeatureState) marketURL(market string) string {
	return s.srv.URL + "/marketplace/" + market + ".json"
}

// pack finds the plugin a marketplace publishes.
func (s *archiveFeatureState) pack(market, plugin string) (*archivePluginPack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.markets[market]
	if m == nil {
		return nil, fmt.Errorf("no marketplace %q in this scenario", market)
	}
	p := m.plugin(plugin)
	if p == nil {
		return nil, fmt.Errorf("marketplace %q publishes no plugin %q", market, plugin)
	}
	return p, nil
}

// packOf finds the marketplace and the plugin that publish plugin.
func (s *archiveFeatureState) packOf(plugin string) (string, *archivePluginPack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, m := range s.markets {
		if p := m.plugin(plugin); p != nil {
			return name, p, nil
		}
	}
	return "", nil, fmt.Errorf("no marketplace publishes %q", plugin)
}

// startServer brings the https marketplace up and lets remote-source downloads
// reach it: the guard passes its address only, and the client trusts its
// certificate.
func (s *archiveFeatureState) startServer() {
	if s.srv != nil {
		return
	}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	host := s.srv.Listener.Addr().String()
	prevGuard, prevTransport := remoteGuard, remoteTransport
	remoteGuard = func(_ context.Context, raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		if u.Host != host {
			return fmt.Errorf("%w: %s", web.ErrDisallowedURL, u.Host)
		}
		return nil
	}
	remoteTransport = s.srv.Client().Transport
	s.restores = append(s.restores, func() { remoteGuard, remoteTransport = prevGuard, prevTransport })
}

// writeHome writes the home's config.yaml and its marketplaces.json, which
// declares sources, and loads the configuration.
func (s *archiveFeatureState) writeHome(sources ...string) error {
	if s.root == "" {
		root, err := os.MkdirTemp("", "coddy-archive-feature-*")
		if err != nil {
			return err
		}
		s.root = root
		s.home = filepath.Join(root, "home")
		if err := os.MkdirAll(s.home, 0o755); err != nil {
			return err
		}
		// The built-in marketplace is a GitHub address; the scenario stays offline.
		prev := SystemSources
		SystemSources = nil
		s.restores = append(s.restores, func() { SystemSources = prev })
	}
	cfgPath := filepath.Join(s.home, "config.yaml")
	body := fmt.Sprintf("skills:\n  dirs:\n    - %q\n", filepath.Join(s.home, "skills"))
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		return err
	}
	if err := config.WriteMarketplacesFile(config.GlobalMarketplacesPath(s.home), config.MarketplacesFile{Sources: sources}); err != nil {
		return err
	}
	cfg, err := config.LoadWithPaths(config.Paths{Home: s.home, ConfigPath: cfgPath, CWD: s.root})
	if err != nil {
		return err
	}
	s.cfg = cfg
	return nil
}

func (s *archiveFeatureState) homeWithoutSources() error { return s.writeHome() }

func (s *archiveFeatureState) homeWithGitMarketplace() error {
	if err := s.writeHome(); err != nil {
		return err
	}
	// A git marketplace that plugin install has no business with: syncing it
	// needs git, which the scenario does not have.
	return s.writeHome("file://" + filepath.ToSlash(filepath.Join(s.root, "git-marketplace")))
}

func (s *archiveFeatureState) sourceInConfig(market string) error {
	s.startServer()
	return s.writeHome(s.marketURL(market))
}

func (s *archiveFeatureState) gitNotInstalled() error {
	empty := filepath.Join(s.root, "bin")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		return err
	}
	prev, had := os.LookupEnv("PATH")
	if err := os.Setenv("PATH", empty); err != nil {
		return err
	}
	s.restores = append(s.restores, func() {
		if had {
			_ = os.Setenv("PATH", prev)
		} else {
			_ = os.Unsetenv("PATH")
		}
	})
	if gitws.GitAvailable() {
		return fmt.Errorf("git is still found on the PATH")
	}
	return nil
}

// publish adds plugins to the marketplace, starting the server and the
// marketplace when they are not up yet.
func (s *archiveFeatureState) publish(market, layout string, names ...string) error {
	s.startServer()
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.markets[market]
	if m == nil {
		m = &archiveMarket{}
		s.markets[market] = m
	}
	for _, name := range names {
		p, err := newPluginPack(name, layout)
		if err != nil {
			return err
		}
		m.plugins = append(m.plugins, p)
	}
	return nil
}

func (s *archiveFeatureState) publishes(market, plugin, layout string) error {
	return s.publish(market, layout, plugin)
}

func (s *archiveFeatureState) publishesTwo(market, first, second string) error {
	return s.publish(market, "", first, second)
}

func (s *archiveFeatureState) alsoPublishes(market, plugin string) error {
	return s.publish(market, "", plugin)
}

// run runs a plugin command, <market> tokens standing for marketplace addresses.
func (s *archiveFeatureState) run(command string) error {
	s.mu.Lock()
	for name := range s.markets {
		command = strings.ReplaceAll(command, "<"+name+">", s.marketURL(name))
	}
	s.mu.Unlock()
	out, err := RunPluginCommand(context.Background(), s.cfg, s.root, strings.Fields(command))
	if err != nil {
		return fmt.Errorf("plugin %s: %w", command, err)
	}
	s.answer = out
	return nil
}

// runInTerminal runs a plugin command the way `coddy plugin` does, which
// may approve a project marketplace.
func (s *archiveFeatureState) runInTerminal(command string) error {
	s.mu.Lock()
	for name := range s.markets {
		command = strings.ReplaceAll(command, "<"+name+">", s.marketURL(name))
	}
	s.mu.Unlock()
	out, err := RunPluginCommandWith(context.Background(), s.cfg, s.root, strings.Fields(command), PluginOptions{AllowTrust: true})
	if err != nil {
		return fmt.Errorf("plugin %s: %w", command, err)
	}
	s.answer = out
	return nil
}

// projectDeclares writes the workspace's .coddy/marketplaces.json with the
// marketplace's address as a source, the way a checkout brings it.
func (s *archiveFeatureState) projectDeclares(market string) error {
	return config.WriteMarketplacesFile(config.ProjectMarketplacesPath(s.root), config.MarketplacesFile{Sources: []string{s.marketURL(market)}})
}

// operatorAddsToProject declares the source in the project's file through the
// API the CLI's `skills add --project` and Settings use.
func (s *archiveFeatureState) operatorAddsToProject(market string) error {
	_, err := AddSource(s.cfg, s.root, s.marketURL(market), ScopeLocal)
	return err
}

// legacyConfig writes a config.yaml that still lists the source in
// skills.sources.
func (s *archiveFeatureState) legacyConfig(market string) error {
	body := fmt.Sprintf("skills:\n  dirs:\n    - %q\n  sources:\n    - %q\n", filepath.Join(s.home, "skills"), s.marketURL(market))
	return os.WriteFile(filepath.Join(s.home, "config.yaml"), []byte(body), 0o644)
}

func (s *archiveFeatureState) loadConfig() error {
	cfg, err := config.LoadWithPaths(config.Paths{Home: s.home, ConfigPath: filepath.Join(s.home, "config.yaml"), CWD: s.root})
	if err != nil {
		return err
	}
	s.cfg = cfg
	return nil
}

func (s *archiveFeatureState) homeDeclaresSource(market string) error {
	file, err := config.ReadMarketplacesFile(config.GlobalMarketplacesPath(s.home))
	if err != nil {
		return err
	}
	for _, src := range file.Sources {
		if sameSource(src, s.marketURL(market)) {
			return nil
		}
	}
	return fmt.Errorf("the home marketplaces.json declares %v, not %s", file.Sources, s.marketURL(market))
}

func (s *archiveFeatureState) configHasNoSources() error {
	data, err := os.ReadFile(filepath.Join(s.home, "config.yaml"))
	if err != nil {
		return err
	}
	if strings.Contains(string(data), "sources") {
		return fmt.Errorf("config.yaml still lists sources:\n%s", data)
	}
	return nil
}

func (s *archiveFeatureState) runForMarketplace(command, market string) error {
	return s.run(command + " <" + market + ">")
}

func (s *archiveFeatureState) answers(want string) error {
	if !strings.Contains(s.answer, want) {
		return fmt.Errorf("the plugin command answered %q, want it to say %q", s.answer, want)
	}
	return nil
}

func (s *archiveFeatureState) skillDir(name string) string {
	return filepath.Join(s.cfg.Skills.ManagedDir(s.home), name)
}

func (s *archiveFeatureState) installed(name string) error {
	if _, err := os.Stat(filepath.Join(s.skillDir(name), "SKILL.md")); err != nil {
		return fmt.Errorf("skill %q is not installed: %w", name, err)
	}
	if _, ok := readRemoteLock(s.cfg.Skills.ManagedDir(s.home))[name]; !ok {
		return fmt.Errorf("the lock has no entry for %q", name)
	}
	return nil
}

func (s *archiveFeatureState) installedWithScript(name, script string) error {
	if err := s.installed(name); err != nil {
		return err
	}
	fi, err := os.Stat(filepath.Join(s.skillDir(name), filepath.FromSlash(script)))
	if err != nil {
		return fmt.Errorf("skill %q came without %s: %w", name, script, err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s lost its executable bit: %v", script, fi.Mode())
	}
	return nil
}

func (s *archiveFeatureState) notInstalled(name string) error {
	if _, err := os.Stat(s.skillDir(name)); !os.IsNotExist(err) {
		return fmt.Errorf("skill %q is installed (%v)", name, err)
	}
	if _, ok := readRemoteLock(s.cfg.Skills.ManagedDir(s.home))[name]; ok {
		return fmt.Errorf("the lock has an entry for %q", name)
	}
	return nil
}

func (s *archiveFeatureState) nothingInstalled() error {
	if lock := readRemoteLock(s.cfg.Skills.ManagedDir(s.home)); len(lock) != 0 {
		return fmt.Errorf("the lock records installed skills: %+v", lock)
	}
	entries, err := os.ReadDir(s.cfg.Skills.ManagedDir(s.home))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			return fmt.Errorf("skill %q is installed", e.Name())
		}
	}
	return nil
}

func (s *archiveFeatureState) lockRecords(name, market string) error {
	p, err := s.pack(market, name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	sum := sha256Hex(p.archive)
	s.mu.Unlock()
	ent, ok := readRemoteLock(s.cfg.Skills.ManagedDir(s.home))[name]
	if !ok {
		return fmt.Errorf("the lock has no entry for %q", name)
	}
	want := RemoteEntry{
		Source:  s.marketURL(market),
		URL:     s.marketURL(market),
		Plugin:  p.name,
		Archive: s.srv.URL + "/plugins/" + market + "/" + p.name + ".zip",
		Version: "sha256:" + sum[:12],
	}
	if ent != want {
		return fmt.Errorf("lock entry for %q = %+v, want %+v", name, ent, want)
	}
	return nil
}

func (s *archiveFeatureState) publishesNewArchive(market, plugin string) error {
	p, err := s.pack(market, plugin)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p.revision++
	return p.build()
}

func (s *archiveFeatureState) updateStatus(name string) (UpdateStatus, error) {
	statuses, err := CheckUpdates(context.Background(), s.cfg, "")
	if err != nil {
		return UpdateStatus{}, err
	}
	for _, st := range statuses {
		if st.Name == name {
			return st, nil
		}
	}
	return UpdateStatus{}, fmt.Errorf("the update check does not list %q: %+v", name, statuses)
}

func (s *archiveFeatureState) updateOffered(name string) error {
	_, p, err := s.packOf(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	want := "sha256:" + sha256Hex(p.archive)[:12]
	s.mu.Unlock()
	st, err := s.updateStatus(name)
	if err != nil {
		return err
	}
	if !st.UpdateAvailable || st.Latest != want {
		return fmt.Errorf("update check for %q = %+v, want an update to %s", name, st, want)
	}
	return nil
}

func (s *archiveFeatureState) noUpdateOffered(name string) error {
	st, err := s.updateStatus(name)
	if err != nil {
		return err
	}
	if st.UpdateAvailable {
		return fmt.Errorf("update check for %q still offers an update: %+v", name, st)
	}
	return nil
}

func (s *archiveFeatureState) fromNewArchive(name string) error {
	_, p, err := s.packOf(name)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(s.skillDir(name), "SKILL.md"))
	if err != nil {
		return err
	}
	s.mu.Lock()
	want := fmt.Sprintf("revision %d", p.revision)
	s.mu.Unlock()
	if !strings.Contains(string(data), want) {
		return fmt.Errorf("SKILL.md of %q on disk is not from the new archive (want %q):\n%s", name, want, data)
	}
	return nil
}

func initializeArchiveScenario(sc *godog.ScenarioContext) {
	s := &archiveFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy home that also names a git marketplace$`, s.homeWithGitMarketplace)
	sc.Step(`^a coddy home without skill sources$`, s.homeWithoutSources)
	sc.Step(`^git is not installed$`, s.gitNotInstalled)
	sc.Step(`^an https marketplace "([^"]*)" publishing the plugin "([^"]*)" as a zip archive (with the plugin at its root|wrapped in one folder|with its sha256|of its author's repository with a SKILL\.md in its test data|with its skill at the plugin root)$`, s.publishes)
	sc.Step(`^an https marketplace "([^"]*)" publishing the plugins "([^"]*)" and "([^"]*)" as zip archives$`, s.publishesTwo)
	sc.Step(`^the marketplace "([^"]*)" also publishes the plugin "([^"]*)" as a zip archive$`, s.alsoPublishes)
	sc.Step(`^the address of the marketplace "([^"]*)" is a source of the home marketplaces\.json$`, s.sourceInConfig)
	sc.Step(`^I (?:have )?run the plugin command "([^"]*)" for the marketplace "([^"]*)"$`, s.runForMarketplace)
	sc.Step(`^I run the plugin command "([^"]*)" in a terminal$`, s.runInTerminal)
	sc.Step(`^I (?:have )?run the plugin command "([^"]*)"$`, s.run)
	sc.Step(`^the workspace's marketplaces\.json declares the marketplace "([^"]*)" as a source$`, s.projectDeclares)
	sc.Step(`^the operator adds the marketplace "([^"]*)" to the workspace's marketplaces\.json$`, s.operatorAddsToProject)
	sc.Step(`^a config\.yaml whose skills\.sources names the marketplace "([^"]*)"$`, s.legacyConfig)
	sc.Step(`^coddy loads the configuration$`, s.loadConfig)
	sc.Step(`^the home marketplaces\.json declares the marketplace "([^"]*)" as a source$`, s.homeDeclaresSource)
	sc.Step(`^config\.yaml no longer has skills\.sources$`, s.configHasNoSources)
	sc.Step(`^the plugin command answers "([^"]*)"$`, s.answers)
	sc.Step(`^the skill "([^"]*)" is installed$`, s.installed)
	sc.Step(`^the skill "([^"]*)" is installed with its executable script "([^"]*)"$`, s.installedWithScript)
	sc.Step(`^the skill "([^"]*)" is not installed$`, s.notInstalled)
	sc.Step(`^no skill is installed$`, s.nothingInstalled)
	sc.Step(`^the lock records the skill "([^"]*)" from the marketplace "([^"]*)" at the version of its archive$`, s.lockRecords)
	sc.Step(`^the marketplace "([^"]*)" publishes a new archive of the plugin "([^"]*)"$`, s.publishesNewArchive)
	sc.Step(`^the update check offers the skill "([^"]*)" at the version of the new archive$`, s.updateOffered)
	sc.Step(`^the skill "([^"]*)" on disk comes from the new archive$`, s.fromNewArchive)
	sc.Step(`^the update check offers no update for the skill "([^"]*)"$`, s.noUpdateOffered)
}

func runArchiveFeature(t *testing.T, name, path string) {
	suite := godog.TestSuite{
		Name:                name,
		ScenarioInitializer: initializeArchiveScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{path},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatalf("%s feature failed", name)
	}
}

func TestPluginArchiveSourceFeature(t *testing.T) {
	runArchiveFeature(t, "plugin-archive-source", "../../features/plugin_archive_source.feature")
}

func TestPluginMarketplaceCommandsFeature(t *testing.T) {
	runArchiveFeature(t, "plugin-marketplace-commands", "../../features/plugin_marketplace_commands.feature")
}

func TestSkillsMarketplaceTrustFeature(t *testing.T) {
	runArchiveFeature(t, "skills-marketplace-trust", "../../features/skills_marketplace_trust.feature")
}
