package skills

// Edge cases of added marketplaces. The happy path is
// features/plugin_marketplace_commands.feature; what is here is what the
// commands must refuse or keep apart: a marketplace without a usable name or
// without a marketplace.json, two marketplaces under one name, a plugin or a
// marketplace that is not there, a plugin a marketplace dropped, the updates
// that must not install what was never installed, and a registry file that
// does not parse.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// testCatalogue serves a marketplace over https: /marketplace.json names its
// plugins, each an archive at /p/<plugin>.zip.
type testCatalogue struct {
	srv *httptest.Server

	mu       sync.Mutex
	name     string
	archives map[string][]byte
	// reads counts the requests for /marketplace.json.
	reads int
}

func newCatalogue(t *testing.T, name string, plugins ...string) *testCatalogue {
	t.Helper()
	c := &testCatalogue{name: name, archives: map[string][]byte{}}
	for _, p := range plugins {
		c.set(p, pluginArchive(t, p, 1))
	}
	c.srv = httptest.NewTLSServer(http.HandlerFunc(c.serve))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *testCatalogue) serve(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r.URL.Path == "/marketplace.json" {
		c.reads++
		names := make([]string, 0, len(c.archives))
		for n := range c.archives {
			names = append(names, n)
		}
		sort.Strings(names)
		plugins := make([]any, 0, len(names))
		for _, n := range names {
			plugins = append(plugins, map[string]any{
				"name":   n,
				"source": map[string]string{"source": "archive", "url": "https://" + r.Host + "/p/" + n + ".zip"},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": c.name, "plugins": plugins})
		return
	}
	if data, ok := c.archives[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/p/"), ".zip")]; ok {
		_, _ = w.Write(data)
		return
	}
	http.NotFound(w, r)
}

func (c *testCatalogue) url() string { return c.srv.URL + "/marketplace.json" }

func (c *testCatalogue) manifestReads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func (c *testCatalogue) set(plugin string, archive []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.archives[plugin] = archive
}

func (c *testCatalogue) drop(plugin string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.archives, plugin)
}

// pluginArchive is a plugin archive holding the one skill named after it.
func pluginArchive(t *testing.T, name string, revision int) []byte {
	return mustZip(t,
		zipEntry{name: ".claude-plugin/plugin.json", body: `{"name":"` + name + `"}`},
		zipEntry{name: "skills/" + name + "/SKILL.md", body: fmt.Sprintf("---\nname: %s\ndescription: d\n---\n\nrevision %d\n", name, revision)},
	)
}

// marketHome is a home with a config file whose marketplaces.json declares
// sources (none by default), off the network.
func marketHome(t *testing.T, sources ...string) *config.Config {
	t.Helper()
	offlineSystemSources(t)
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("agent:\n  max_turns: 5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	declareHomeSources(t, home, sources...)
	return &config.Config{
		Paths:  config.Paths{Home: home, ConfigPath: cfgPath},
		Skills: config.Skills{Dirs: []string{filepath.Join(home, "skills")}},
	}
}

// declareHomeSources makes <home>/marketplaces.json declare sources as
// installed whole, keeping its marketplaces.
func declareHomeSources(t *testing.T, home string, sources ...string) {
	t.Helper()
	path := config.GlobalMarketplacesPath(home)
	file, err := config.ReadMarketplacesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file.Sources = append([]string(nil), sources...)
	if err := config.WriteMarketplacesFile(path, file); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, cfg *config.Config, words ...string) (string, error) {
	t.Helper()
	return RunPluginCommand(context.Background(), cfg, ".", words)
}

func mustRun(t *testing.T, cfg *config.Config, words ...string) string {
	t.Helper()
	out, err := run(t, cfg, words...)
	if err != nil {
		t.Fatalf("plugin %s: %v", strings.Join(words, " "), err)
	}
	return out
}

func installedSkills(t *testing.T, cfg *config.Config) []string {
	t.Helper()
	var out []string
	for name := range readRemoteLock(cfg.Skills.ManagedDir(cfg.Paths.Home)) {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func TestParsePluginRef(t *testing.T) {
	ok := map[string][2]string{
		"yandex-wordstat@neuraldeep": {"yandex-wordstat", "neuraldeep"},
		"rpa-feat@rpa-skills":        {"rpa-feat", "rpa-skills"},
		" a.b_c@M1 ":                 {"a.b_c", "M1"},
	}
	for in, want := range ok {
		p, m, got := ParsePluginRef(in)
		if !got || p != want[0] || m != want[1] {
			t.Errorf("ParsePluginRef(%q) = %q, %q, %v", in, p, m, got)
		}
	}
	for _, in := range []string{
		"owner/repo",
		"owner/repo@v1.2",
		"git@github.com:owner/repo.git",
		"https://neuraldeep.ru/skapi/marketplace.json",
		"plugin@",
		"@market",
		"a@b@c",
		"-flag@market",
		"plugin@-market",
		"",
	} {
		if _, _, got := ParsePluginRef(in); got {
			t.Errorf("ParsePluginRef(%q) took it for <plugin>@<marketplace>", in)
		}
	}
}

func TestAddMarketplaceRefusesWhatItCannotName(t *testing.T) {
	unnamed := newCatalogue(t, "", "a")
	spaced := newCatalogue(t, "two words", "a")
	reachRemote(t, unnamed.srv, spaced.srv)
	cfg := marketHome(t)
	for _, c := range []*testCatalogue{unnamed, spaced} {
		_, err := run(t, cfg, "marketplace", "add", c.url())
		if err == nil || !strings.Contains(err.Error(), "`plugin install "+c.url()+"`") {
			t.Errorf("add %q-named marketplace: %v, want a refusal naming the whole install", c.name, err)
		}
	}
	if ms, _ := AddedMarketplaces(cfg, ""); len(ms) != 0 {
		t.Errorf("added %+v", ms)
	}
}

func TestAddMarketplaceWithoutAManifestPointsAtInstall(t *testing.T) {
	if !gitws.GitAvailable() {
		t.Skip("git binary not available")
	}
	cfg := marketHome(t)
	repo := t.TempDir()
	writeSkill(t, filepath.Join(repo, "skills", "loose"), "loose")
	gitCommitAllRepo(t, repo, true, "loose skill")
	src := "file://" + filepath.ToSlash(repo)
	_, err := run(t, cfg, "marketplace", "add", src)
	if err == nil || !strings.Contains(err.Error(), "publishes no marketplace.json") || !strings.Contains(err.Error(), "`plugin install "+src+"`") {
		t.Fatalf("add a repo without marketplace.json: %v", err)
	}
}

func TestAddMarketplaceKeepsOneMarketplacePerName(t *testing.T) {
	first := newCatalogue(t, "shop", "a")
	second := newCatalogue(t, "shop", "b")
	reachRemote(t, first.srv, second.srv)
	cfg := marketHome(t)
	mustRun(t, cfg, "marketplace", "add", first.url())
	_, err := run(t, cfg, "marketplace", "add", second.url())
	if err == nil || !strings.Contains(err.Error(), "`plugin marketplace remove shop`") {
		t.Fatalf("a second marketplace named shop: %v", err)
	}
	ms, _ := AddedMarketplaces(cfg, "")
	if len(ms) != 1 || ms[0].Source != first.url() {
		t.Fatalf("added = %+v, want the first one kept", ms)
	}
	// Once the first is removed, the second takes the name.
	if out := mustRun(t, cfg, "marketplace", "remove", "shop"); !strings.Contains(out, `Removed marketplace "shop"`) {
		t.Fatalf("remove: %q", out)
	}
	mustRun(t, cfg, "marketplace", "add", second.url())
	if ms, _ := AddedMarketplaces(cfg, ""); len(ms) != 1 || ms[0].Source != second.url() {
		t.Fatalf("added = %+v, want the second one", ms)
	}
}

func TestInstallFromMarketplaceSaysWhatToDo(t *testing.T) {
	c := newCatalogue(t, "neuraldeep", "a", "b")
	reachRemote(t, c.srv)
	cfg := marketHome(t)

	_, err := run(t, cfg, "install", "a@neuraldeep")
	if err == nil || !strings.Contains(err.Error(), `marketplace "neuraldeep" is not added`) || !strings.Contains(err.Error(), "`plugin marketplace add") {
		t.Fatalf("install from a marketplace not added: %v", err)
	}

	mustRun(t, cfg, "marketplace", "add", c.url())
	_, err = run(t, cfg, "install", "a@other")
	if err == nil || !strings.Contains(err.Error(), "(added: neuraldeep)") {
		t.Fatalf("install from another marketplace: %v, want the added ones named", err)
	}
	_, err = run(t, cfg, "install", "missing@neuraldeep")
	if err == nil || !strings.Contains(err.Error(), `plugin "missing" is not in marketplace "neuraldeep"`) ||
		!strings.Contains(err.Error(), "2 plugin(s)") || !strings.Contains(err.Error(), "`plugin marketplace list neuraldeep`") {
		t.Fatalf("install a plugin the marketplace does not list: %v", err)
	}
	if got := installedSkills(t, cfg); len(got) != 0 {
		t.Fatalf("installed %v", got)
	}

	c.srv.Close()
	_, err = run(t, cfg, "install", "a@neuraldeep")
	if err == nil || !strings.Contains(err.Error(), `read marketplace "neuraldeep"`) {
		t.Fatalf("install while the marketplace is down: %v", err)
	}
}

func TestInstallFromMarketplaceFailingIsAnError(t *testing.T) {
	c := newCatalogue(t, "neuraldeep", "a")
	c.set("a", []byte("not a zip"))
	reachRemote(t, c.srv)
	cfg := marketHome(t)
	mustRun(t, cfg, "marketplace", "add", c.url())
	if _, err := run(t, cfg, "install", "a@neuraldeep"); err == nil || !strings.Contains(err.Error(), "install a@neuraldeep") {
		t.Fatalf("an archive that does not unpack: %v, want an error", err)
	}
}

func TestUpdatesTouchOnlyWhatWasInstalled(t *testing.T) {
	c := newCatalogue(t, "neuraldeep", "a", "b")
	reachRemote(t, c.srv)

	t.Run("sync of everything", func(t *testing.T) {
		cfg := marketHome(t)
		mustRun(t, cfg, "marketplace", "add", c.url())
		mustRun(t, cfg, "install", "a@neuraldeep")
		c.set("a", pluginArchive(t, "a", 2))
		out := mustRun(t, cfg, "marketplace", "sync")
		if !strings.Contains(out, "0 added, 1 updated, 0 failed.") {
			t.Fatalf("sync answered %q", out)
		}
		if got := installedSkills(t, cfg); strings.Join(got, ",") != "a" {
			t.Fatalf("installed %v, want only a", got)
		}
	})
	t.Run("the update button of one skill", func(t *testing.T) {
		cfg := marketHome(t)
		mustRun(t, cfg, "marketplace", "add", c.url())
		mustRun(t, cfg, "install", "a@neuraldeep")
		res, err := UpdateSkill(context.Background(), cfg, "", "a")
		if err != nil || len(res.Failed) != 0 || strings.Join(res.Updated, ",") != "a" {
			t.Fatalf("UpdateSkill = %+v, %v", res, err)
		}
		if got := installedSkills(t, cfg); strings.Join(got, ",") != "a" {
			t.Fatalf("installed %v, want only a", got)
		}
	})
	t.Run("a plugin the marketplace dropped", func(t *testing.T) {
		cfg := marketHome(t)
		mustRun(t, cfg, "marketplace", "add", c.url())
		mustRun(t, cfg, "install", "b@neuraldeep")
		c.drop("b")
		t.Cleanup(func() { c.set("b", pluginArchive(t, "b", 1)) })
		out := mustRun(t, cfg, "marketplace", "update", "neuraldeep")
		if !strings.Contains(out, "1 failed") || !strings.Contains(out, `plugin "b" is no longer in the marketplace`) {
			t.Fatalf("update answered %q", out)
		}
		if got := installedSkills(t, cfg); strings.Join(got, ",") != "b" {
			t.Fatalf("installed %v, want b kept", got)
		}
	})
	t.Run("an address nobody added", func(t *testing.T) {
		cfg := marketHome(t)
		if _, err := run(t, cfg, "marketplace", "update", "nothing-here"); err == nil || !strings.Contains(err.Error(), `marketplace "nothing-here" is not added`) {
			t.Fatalf("update of an unknown name: %v", err)
		}
	})
}

func TestAWholeSourceSyncsWholeByItsMarketplaceName(t *testing.T) {
	c := newCatalogue(t, "neuraldeep", "a", "b")
	reachRemote(t, c.srv)
	cfg := marketHome(t, c.url())
	mustRun(t, cfg, "marketplace", "add", c.url())
	out := mustRun(t, cfg, "marketplace", "update", "neuraldeep")
	if !strings.Contains(out, "2 added") {
		t.Fatalf("update by name of a marketplace that is a source too answered %q, want every plugin installed", out)
	}
	if got := installedSkills(t, cfg); strings.Join(got, ",") != "a,b" {
		t.Fatalf("installed %v", got)
	}
}

func TestASourceInConfigStaysWhole(t *testing.T) {
	c := newCatalogue(t, "neuraldeep", "a", "b")
	reachRemote(t, c.srv)
	cfg := marketHome(t, c.url())
	out := mustRun(t, cfg, "marketplace", "add", c.url())
	if !strings.Contains(out, "every plugin it lists is installed") {
		t.Fatalf("adding a configured source answered %q", out)
	}
	out = mustRun(t, cfg, "marketplace", "update", c.url())
	if !strings.Contains(out, "2 added") {
		t.Fatalf("updating a configured source answered %q, want every plugin installed", out)
	}
	if got := installedSkills(t, cfg); strings.Join(got, ",") != "a,b" {
		t.Fatalf("installed %v", got)
	}
}

func TestMarketplaceListAndShow(t *testing.T) {
	c := newCatalogue(t, "neuraldeep", "a", "b")
	reachRemote(t, c.srv)
	cfg := marketHome(t)
	if out := mustRun(t, cfg, "marketplace", "list"); !strings.Contains(out, "No marketplaces configured") {
		t.Fatalf("empty list: %q", out)
	}
	mustRun(t, cfg, "marketplace", "add", c.url())
	mustRun(t, cfg, "install", "b@neuraldeep")
	out := mustRun(t, cfg, "marketplace", "list")
	if !strings.Contains(out, "1 added marketplace(s)") || !strings.Contains(out, "- neuraldeep  [valid marketplace — neuraldeep, 2 plugin(s); "+c.url()+"; yours]") {
		t.Fatalf("list: %q", out)
	}
	out = mustRun(t, cfg, "marketplace", "list", "neuraldeep")
	if !strings.Contains(out, "  - a\n") || !strings.Contains(out, "  - b  [installed]") || !strings.Contains(out, "`plugin install <plugin>@neuraldeep`") {
		t.Fatalf("show: %q", out)
	}
	if _, err := run(t, cfg, "marketplace", "list", "other"); err == nil {
		t.Fatal("showing a marketplace nobody added succeeded")
	}
	// Removing by address works as by name, and leaves installed skills be.
	if out := mustRun(t, cfg, "marketplace", "remove", c.url()); !strings.Contains(out, `Removed marketplace "neuraldeep"`) {
		t.Fatalf("remove by address: %q", out)
	}
	if got := installedSkills(t, cfg); strings.Join(got, ",") != "b" {
		t.Fatalf("installed %v after remove, want b kept", got)
	}
	if out := mustRun(t, cfg, "marketplace", "remove", "neuraldeep"); !strings.Contains(out, "was not configured") {
		t.Fatalf("second remove: %q", out)
	}
}

func TestAvailablePluginsListsAddedMarketplaces(t *testing.T) {
	c := newCatalogue(t, "neuraldeep", "a", "b")
	reachRemote(t, c.srv)
	cfg := marketHome(t)
	mustRun(t, cfg, "marketplace", "add", c.url())
	mustRun(t, cfg, "install", "a@neuraldeep")
	avail, err := AvailablePlugins(context.Background(), cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range avail {
		if p.Source != c.url() {
			t.Errorf("plugin %q from %q, want the added marketplace", p.Name, p.Source)
		}
		got[p.Name] = p.Installed
	}
	if len(got) != 2 || !got["a"] || got["b"] {
		t.Fatalf("available = %+v, want a installed and b not", avail)
	}
}

func TestACorruptMarketplaceListIsNotOverwritten(t *testing.T) {
	c := newCatalogue(t, "neuraldeep", "a")
	reachRemote(t, c.srv)
	cfg := marketHome(t)
	managed := cfg.Skills.ManagedDir(cfg.Paths.Home)
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(managed, marketplacesFile)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, cfg, "marketplace", "add", c.url()); err == nil {
		t.Fatal("add over a corrupt list succeeded")
	}
	if data, _ := os.ReadFile(path); string(data) != "{not json" {
		t.Fatalf("the corrupt list was overwritten: %q", data)
	}
	res, err := Sync(context.Background(), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 1 || res.Failed[0].Source != marketplacesFile {
		t.Fatalf("sync = %+v, want the list reported", res)
	}
}

func TestSameSource(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"owner/repo", "https://github.com/owner/repo", true},
		{"owner/repo", "https://github.com/owner/repo.git", true},
		{"https://example.com/marketplace.json", "https://example.com/marketplace.json/", true},
		{"HTTPS://Example.com/marketplace.json", "https://example.com/marketplace.json", true},
		{"owner/repo@v1", "owner/repo", false},
		{"https://example.com/a.json", "https://example.com/b.json", false},
		{"neuraldeep", "https://neuraldeep.ru/skapi/marketplace.json", false},
	}
	for _, tc := range cases {
		if got := sameSource(tc.a, tc.b); got != tc.want {
			t.Errorf("sameSource(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestAnAddedMarketplaceAnswersToAnySpellingOfItsSource(t *testing.T) {
	c := newCatalogue(t, "neuraldeep", "a", "b")
	reachRemote(t, c.srv)
	cfg := marketHome(t)
	mustRun(t, cfg, "marketplace", "add", c.url())
	mustRun(t, cfg, "install", "a@neuraldeep")
	out := mustRun(t, cfg, "marketplace", "update", c.url()+"/")
	if !strings.Contains(out, `Updated marketplace "neuraldeep"`) || !strings.Contains(out, "0 added, 1 updated") {
		t.Fatalf("update by another spelling answered %q, want the added marketplace updated", out)
	}
	if got := installedSkills(t, cfg); strings.Join(got, ",") != "a" {
		t.Fatalf("installed %v, want only a", got)
	}
}

func TestRemovingAMarketplaceByNameTakesItsSourceOut(t *testing.T) {
	c := newCatalogue(t, "neuraldeep", "a")
	reachRemote(t, c.srv)
	cfg := marketHome(t, c.url())
	mustRun(t, cfg, "marketplace", "add", c.url())
	out := mustRun(t, cfg, "marketplace", "remove", "neuraldeep")
	if !strings.Contains(out, "and its source") {
		t.Fatalf("remove answered %q", out)
	}
	if got := ListSources(cfg, ""); len(got) != 0 {
		t.Fatalf("sources = %v, want the marketplace's source gone too", got)
	}
}

// trustHome is a home off the network whose operator file declares home and
// whose workspace declares project, under policy.
func trustHome(t *testing.T, policy string, home, project config.MarketplacesFile) (*config.Config, string) {
	t.Helper()
	offlineSystemSources(t)
	SystemSources = []string{"system/market"}
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}}
	cfg.Skills.ProjectTrust = policy
	cwd := t.TempDir()
	if err := config.WriteMarketplacesFile(config.GlobalMarketplacesPath(cfg.Paths.Home), home); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteMarketplacesFile(config.ProjectMarketplacesPath(cwd), project); err != nil {
		t.Fatal(err)
	}
	return cfg, cwd
}

// What a workspace has comes in order - the system source, the operator's
// file, the project's - each entry once: a project repeating the operator's
// source or marketplace name adds nothing, and a project entry under ask
// waits for its approval.
func TestDeclarationsComeInOrderEachOnce(t *testing.T) {
	cfg, cwd := trustHome(t, "",
		config.MarketplacesFile{Sources: []string{"owner/home", "https://github.com/system/market"}, Marketplaces: []config.DeclaredMarketplace{{Name: "shop", Source: "owner/shop"}}},
		config.MarketplacesFile{Sources: []string{"Owner/Home", "owner/project"}, Marketplaces: []config.DeclaredMarketplace{{Name: "shop", Source: "owner/elsewhere"}, {Name: "team", Source: "owner/team"}}},
	)
	decls, errs := Declarations(cfg, cwd)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	var got []string
	for _, d := range decls {
		got = append(got, d.Origin+":"+d.Kind+":"+d.Key()+":"+d.State)
	}
	want := []string{
		"system:source:system/market:ready",
		"home:source:owner/home:ready",
		"home:marketplace:shop:ready",
		"project:source:owner/project:needs_approval",
		"project:marketplace:team:needs_approval",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("declarations:\n%v\nwant:\n%v", got, want)
	}
	if got := ListSources(cfg, cwd); !reflect.DeepEqual(got, []string{"system/market", "owner/home"}) {
		t.Fatalf("sources in effect = %v", got)
	}
}

// An approval binds the entry as it was shown: a checkout that rewrites the
// address asks again, and an approval of what the operator saw before the
// rewrite is refused. allow needs no approval, deny reads nothing into effect.
func TestProjectEntryTrustFollowsTheDeclaration(t *testing.T) {
	cfg, cwd := trustHome(t, config.ProjectTrustAsk, config.MarketplacesFile{}, config.MarketplacesFile{Sources: []string{"owner/project"}})
	SystemSources = nil // the sync below must not reach out for it
	shown, ok := FindDeclaration(cfg, cwd, "owner/project")
	if !ok || shown.State != StateNeedsApproval || shown.Fingerprint == "" {
		t.Fatalf("project entry = %+v, %v", shown, ok)
	}
	if _, err := ApproveShown(cfg, cwd, "owner/project", shown.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if got := ListSources(cfg, cwd); !reflect.DeepEqual(got, []string{"owner/project"}) {
		t.Fatalf("sources after the approval = %v", got)
	}
	if _, err := ApproveShown(cfg, cwd, "owner/project", "sha256:stale"); !errors.Is(err, ErrDeclarationChanged) {
		t.Fatalf("approving a stale fingerprint: %v", err)
	}

	// The checkout points the entry somewhere else: no approval covers it.
	if err := config.WriteMarketplacesFile(config.ProjectMarketplacesPath(cwd), config.MarketplacesFile{Sources: []string{"attacker/project"}}); err != nil {
		t.Fatal(err)
	}
	if d, _ := FindDeclaration(cfg, cwd, "attacker/project"); d.State != StateNeedsApproval {
		t.Fatalf("a rewritten entry = %+v, want it awaiting approval", d)
	}
	res, err := Sync(context.Background(), cfg, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Held) != 1 || res.Held[0].Source != "attacker/project" || len(res.Failed) != 0 {
		t.Fatalf("sync = %+v, want the rewritten entry held and nothing fetched", res)
	}

	cfg.Skills.ProjectTrust = config.ProjectTrustAllow
	if got := ListSources(cfg, cwd); !reflect.DeepEqual(got, []string{"attacker/project"}) {
		t.Fatalf("sources under allow = %v", got)
	}
	cfg.Skills.ProjectTrust = config.ProjectTrustDeny
	if d, _ := FindDeclaration(cfg, cwd, "attacker/project"); d.State != StateDenied {
		t.Fatalf("under deny = %+v", d)
	}
	if _, err := ApproveShown(cfg, cwd, "attacker/project", ""); err == nil {
		t.Fatal("an approval under deny was recorded")
	}
}

// A held project marketplace cannot be installed from, and the refusal says
// how to approve it; removing a project entry takes its approval with it.
func TestHeldProjectMarketplaceAndItsRemoval(t *testing.T) {
	cfg, cwd := trustHome(t, "", config.MarketplacesFile{}, config.MarketplacesFile{Marketplaces: []config.DeclaredMarketplace{{Name: "team", Source: "owner/team"}}})
	_, err := InstallFromMarketplace(context.Background(), cfg, cwd, "lint", "team")
	if err == nil || !strings.Contains(err.Error(), "coddy plugin marketplace trust team") {
		t.Fatalf("installing from a held marketplace: %v", err)
	}
	if _, err := ApproveShown(cfg, cwd, "team", ""); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveSource(cfg, cwd, "team", "")
	if err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	if file, _ := config.ReadMarketplacesFile(config.ProjectMarketplacesPath(cwd)); len(file.Marketplaces) != 0 {
		t.Fatalf("project file after the removal = %+v", file)
	}
	if recs := NewTrustStore(cfg.Paths.Home).Records(mcp.CanonicalWorkspace(cwd)); len(recs) != 0 {
		t.Fatalf("receipts after the removal = %+v", recs)
	}
}

// An older Coddy kept the added marketplaces in the managed dir's list. That
// list moves into the operator's marketplaces.json once, a marketplace the
// file declares already staying as the file has it, and stays as the cache of
// what each one listed.
func TestLegacyMarketplaceListMovesIntoTheHomeFile(t *testing.T) {
	cfg, cwd := trustHome(t, "", config.MarketplacesFile{Marketplaces: []config.DeclaredMarketplace{{Name: "kept", Source: "owner/kept-there"}}}, config.MarketplacesFile{})
	managed := cfg.Skills.ManagedDir(cfg.Paths.Home)
	legacy := `{"marketplaces":[{"name":"neuraldeep","source":"https://neuraldeep.example/marketplace.json","plugins":[{"name":"wordstat"}]},{"name":"kept","source":"owner/kept"}]}`
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managed, marketplacesFile), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	ms, err := AddedMarketplaces(cfg, cwd)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range ms {
		names = append(names, m.Name+"="+m.Source)
	}
	if !reflect.DeepEqual(names, []string{"kept=owner/kept-there", "neuraldeep=https://neuraldeep.example/marketplace.json"}) {
		t.Fatalf("marketplaces after the move = %v", names)
	}
	if ms[1].Plugins == nil || ms[1].Plugins[0].Name != "wordstat" {
		t.Fatalf("the listing of the moved marketplace was lost: %+v", ms[1])
	}
	doc, err := readMarketplacesDoc(managed)
	if err != nil || doc.Version != marketplacesCacheVersion {
		t.Fatalf("cache after the move = %+v, %v", doc, err)
	}
	// Removed from the operator's file, it does not come back from the cache.
	if _, err := RemoveSource(cfg, cwd, "neuraldeep", ""); err != nil {
		t.Fatal(err)
	}
	if ms, _ := AddedMarketplaces(cfg, cwd); len(ms) != 1 {
		t.Fatalf("marketplaces after the removal = %+v", ms)
	}
}

// projectHome is a home with no marketplace of its own and a workspace whose
// .coddy/marketplaces.json is project, under the given policy.
func projectHome(t *testing.T, policy string, project config.MarketplacesFile) (*config.Config, string) {
	t.Helper()
	cfg := marketHome(t)
	cfg.Skills.ProjectTrust = policy
	cwd := t.TempDir()
	if err := config.WriteMarketplacesFile(config.ProjectMarketplacesPath(cwd), project); err != nil {
		t.Fatal(err)
	}
	return cfg, cwd
}

func managedSkillBody(t *testing.T, cfg *config.Config, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cfg.Skills.ManagedDir(cfg.Paths.Home), name, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// An update fetches from the source like a sync, so a project source whose
// approval was withdrawn, or one skills.project_trust: deny switches off, is
// neither updated from nor even read for the update check; the refusal says
// how to approve it.
func TestAHeldProjectSourceIsNeitherUpdatedFromNorChecked(t *testing.T) {
	for _, mode := range []string{"untrust", "deny"} {
		t.Run(mode, func(t *testing.T) {
			c := newCatalogue(t, "team", "lint")
			reachRemote(t, c.srv)
			cfg, cwd := projectHome(t, "", config.MarketplacesFile{Sources: []string{c.url()}})
			if _, err := ApproveShown(cfg, cwd, c.url(), ""); err != nil {
				t.Fatal(err)
			}
			if res, err := Sync(context.Background(), cfg, cwd); err != nil || len(res.Added) != 1 {
				t.Fatalf("sync = %+v, %v", res, err)
			}
			if mode == "untrust" {
				if ok, err := Revoke(cfg, cwd, c.url()); err != nil || !ok {
					t.Fatalf("revoke = %v, %v", ok, err)
				}
			} else {
				cfg.Skills.ProjectTrust = config.ProjectTrustDeny
			}
			c.set("lint", pluginArchive(t, "lint", 2))

			_, err := UpdateSkill(context.Background(), cfg, cwd, "lint")
			if !errors.Is(err, ErrHeld) {
				t.Fatalf("updating from a held source: %v, want ErrHeld", err)
			}
			if mode == "untrust" && !strings.Contains(err.Error(), "coddy plugin marketplace trust") {
				t.Fatalf("the refusal does not say how to approve: %v", err)
			}
			if body := managedSkillBody(t, cfg, "lint"); !strings.Contains(body, "revision 1") {
				t.Fatalf("the held source was fetched: %s", body)
			}

			before := c.manifestReads()
			ups, err := CheckUpdates(context.Background(), cfg, cwd)
			if err != nil || len(ups) != 1 || ups[0].UpdateAvailable {
				t.Fatalf("update check = %+v, %v; want lint with no update", ups, err)
			}
			if c.manifestReads() != before {
				t.Fatal("the update check read a held source")
			}

			// Another workspace declares nothing: the operator may still update
			// what is installed, as an explicit request.
			if _, err := UpdateSkill(context.Background(), cfg, t.TempDir(), "lint"); err != nil {
				t.Fatalf("an update where no file declares the source: %v", err)
			}
			if body := managedSkillBody(t, cfg, "lint"); !strings.Contains(body, "revision 2") {
				t.Fatalf("the update elsewhere installed %s", body)
			}
		})
	}
}

// Removing a project marketplace by its name takes a source of the same
// address out of the file with it, and the approvals of both: a checkout that
// writes the source back is asked about again.
func TestARemovalTakesTheApprovalsOfWhatItRemoved(t *testing.T) {
	cfg, cwd := trustHome(t, "", config.MarketplacesFile{}, config.MarketplacesFile{
		Sources:      []string{"owner/team"},
		Marketplaces: []config.DeclaredMarketplace{{Name: "team", Source: "owner/team"}},
	})
	if _, err := ApproveShown(cfg, cwd, "team", ""); err != nil {
		t.Fatal(err)
	}
	if d, err := ApproveShown(cfg, cwd, "owner/team", ""); err != nil || d.Kind != KindSource {
		t.Fatalf("approving the source = %+v, %v", d, err)
	}
	r, err := RemoveDeclared(cfg, cwd, "team")
	if err != nil || len(r.Marketplaces) != 1 || len(r.Sources) != 1 {
		t.Fatalf("removal = %+v, %v", r, err)
	}
	if recs := NewTrustStore(cfg.Paths.Home).Records(mcp.CanonicalWorkspace(cwd)); len(recs) != 0 {
		t.Fatalf("receipts left behind: %+v", recs)
	}
	if err := config.WriteMarketplacesFile(config.ProjectMarketplacesPath(cwd), config.MarketplacesFile{Sources: []string{"owner/team"}}); err != nil {
		t.Fatal(err)
	}
	if d, _ := FindDeclaration(cfg, cwd, "owner/team"); d.Trusted || d.State != StateNeedsApproval {
		t.Fatalf("a source written back = %+v, want it awaiting approval", d)
	}
}

// A receipts file that does not parse is never read as "nothing approved" and
// never written over: every approval it held is still there once the operator
// repairs it.
func TestADamagedReceiptsFileIsNeverOverwritten(t *testing.T) {
	home := t.TempDir()
	store := NewTrustStore(home)
	one := Declaration{Kind: KindSource, Source: "a/one"}
	if err := store.Approve("/w1", one); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(data), `"version": 1,`, `"version": 1,,`, 1)
	if err := os.WriteFile(store.Path(), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Approve("/w2", Declaration{Kind: KindSource, Source: "b/two"}); err == nil {
		t.Fatal("an approval was written over a damaged receipts file")
	}
	if _, err := store.Revoke("/w1", "a/one"); err == nil {
		t.Fatal("a revocation was written over a damaged receipts file")
	}
	if store.Approved("/w1", declarationDigest(one.Kind, one.Name, one.Source)) {
		t.Fatal("a damaged file still approves")
	}
	if after, _ := os.ReadFile(store.Path()); string(after) != broken {
		t.Fatalf("the damaged file was rewritten:\n%s", after)
	}
}

// Writing an entry into the project approves it as the file spells it: an
// entry the file already declared another way (a git URL for owner/repo,
// another case) is approved, not left held behind a receipt for the typed
// spelling.
func TestAddingToTheProjectApprovesTheEntryAsTheFileHasIt(t *testing.T) {
	for _, have := range []string{"https://github.com/owner/repo", "Owner/Repo"} {
		cfg, cwd := trustHome(t, "", config.MarketplacesFile{}, config.MarketplacesFile{Sources: []string{have}})
		if _, err := AddSource(cfg, cwd, "owner/repo", ScopeLocal); err != nil {
			t.Fatal(err)
		}
		if d, _ := FindDeclaration(cfg, cwd, "owner/repo"); d.State != StateReady {
			t.Fatalf("project file has %q: the entry after skills add --project = %+v", have, d)
		}
	}
}

// A project source at the address of one of the operator's catalogs is
// listed on its own (it would install every plugin, the catalog one by one),
// so it must be approvable: the approval takes the project entry, the one the
// fingerprint names.
func TestAProjectSourceAtTheAddressOfYourCatalogCanBeApproved(t *testing.T) {
	cfg, cwd := trustHome(t, "",
		config.MarketplacesFile{Marketplaces: []config.DeclaredMarketplace{{Name: "shop", Source: "owner/repo"}}},
		config.MarketplacesFile{Sources: []string{"owner/repo"}})
	var shown Declaration
	decls, _ := Declarations(cfg, cwd)
	for _, d := range decls {
		if d.Origin == OriginProject {
			shown = d
		}
	}
	if shown.State != StateNeedsApproval {
		t.Fatalf("the project source = %+v, want it listed awaiting approval", shown)
	}
	d, err := ApproveShown(cfg, cwd, "owner/repo", shown.Fingerprint)
	if err != nil || d.Origin != OriginProject || d.Kind != KindSource {
		t.Fatalf("approval = %+v, %v", d, err)
	}
	if got := ListSources(cfg, cwd); !reflect.DeepEqual(got, []string{"system/market", "owner/repo"}) {
		t.Fatalf("sources in effect after the approval = %v", got)
	}
	// Without a fingerprint the project entry is the one approved as well.
	if _, err := Revoke(cfg, cwd, "owner/repo"); err != nil {
		t.Fatal(err)
	}
	if d, err := ApproveShown(cfg, cwd, "owner/repo", ""); err != nil || d.Origin != OriginProject {
		t.Fatalf("approval without a fingerprint = %+v, %v", d, err)
	}
}

// A client that shows one row per file removes from that file only: your own
// entry leaves your file and the project's checked-in copy stays, and the
// reverse; without an origin both files are edited.
func TestARemovalCanStayInOneFile(t *testing.T) {
	both := config.MarketplacesFile{Sources: []string{"owner/repo"}}
	cfg, cwd := trustHome(t, config.ProjectTrustAllow, both, both)
	if removed, err := RemoveSource(cfg, cwd, "owner/repo", OriginHome); err != nil || !removed {
		t.Fatalf("remove from yours = %v, %v", removed, err)
	}
	home, _ := config.ReadMarketplacesFile(config.GlobalMarketplacesPath(cfg.Paths.Home))
	project, _ := config.ReadMarketplacesFile(config.ProjectMarketplacesPath(cwd))
	if len(home.Sources) != 0 || len(project.Sources) != 1 {
		t.Fatalf("after removing from yours: home %+v, project %+v", home, project)
	}
	declareHomeSources(t, cfg.Paths.Home, "owner/repo")
	if removed, err := RemoveSource(cfg, cwd, "owner/repo", OriginProject); err != nil || !removed {
		t.Fatalf("remove from the project = %v, %v", removed, err)
	}
	home, _ = config.ReadMarketplacesFile(config.GlobalMarketplacesPath(cfg.Paths.Home))
	project, _ = config.ReadMarketplacesFile(config.ProjectMarketplacesPath(cwd))
	if len(home.Sources) != 1 || len(project.Sources) != 0 {
		t.Fatalf("after removing from the project: home %+v, project %+v", home, project)
	}
	if _, err := RemoveSource(cfg, cwd, "owner/repo", "elsewhere"); err == nil {
		t.Fatal("an unknown origin was accepted")
	}
}

// A model can type into a chat, so /plugin cannot approve a project entry:
// it names the terminal and the shield instead, and records nothing.
func TestTheChatCannotApproveAProjectEntry(t *testing.T) {
	cfg, cwd := trustHome(t, "", config.MarketplacesFile{}, config.MarketplacesFile{Sources: []string{"owner/project"}})
	for _, verb := range []string{"trust", "untrust"} {
		_, err := RunPluginCommand(context.Background(), cfg, cwd, []string{"marketplace", verb, "owner/project"})
		if err == nil || !strings.Contains(err.Error(), "coddy plugin marketplace trust owner/project") {
			t.Fatalf("/plugin marketplace %s: %v", verb, err)
		}
	}
	if recs := NewTrustStore(cfg.Paths.Home).Records(mcp.CanonicalWorkspace(cwd)); len(recs) != 0 {
		t.Fatalf("the chat recorded receipts: %+v", recs)
	}
	if d, _ := FindDeclaration(cfg, cwd, "owner/project"); d.State != StateNeedsApproval {
		t.Fatalf("after the chat tried = %+v", d)
	}
	// The terminal can.
	out, err := RunPluginCommandWith(context.Background(), cfg, cwd, []string{"marketplace", "trust", "owner/project"}, PluginOptions{AllowTrust: true})
	if err != nil || !strings.Contains(out, "Approved") {
		t.Fatalf("coddy plugin marketplace trust = %q, %v", out, err)
	}
}
