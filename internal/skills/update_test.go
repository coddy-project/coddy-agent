package skills

// Edge cases of reading the lists and updating. The happy path is
// features/plugin_marketplace_commands.feature and the delivery's in
// features/skills_delivery.feature; what is here is what an update must leave
// alone or refuse: a copy newer than the published one, the skill of a plugin
// the operator removed, a delivered skill the catalogue does not publish, a
// held project entry, and the version only the second manifest of a
// repository names.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
)

// gitCatalogue is a git marketplace on disk: plugins under plugins/<name>,
// each holding the skills it names at the version it is published at.
type gitCatalogue struct {
	t       *testing.T
	repo    string
	inited  bool
	plugins map[string]gitPlugin
}

type gitPlugin struct {
	version string
	skills  []string
}

func newGitCatalogue(t *testing.T) *gitCatalogue {
	t.Helper()
	if !gitws.GitAvailable() {
		t.Skip("git binary not available")
	}
	return &gitCatalogue{t: t, repo: t.TempDir(), plugins: map[string]gitPlugin{}}
}

func (c *gitCatalogue) url() string { return "file://" + filepath.ToSlash(c.repo) }

// publish writes plugin at version with its skills and commits it. The
// manifest the loader prefers names no versions; the other one does.
func (c *gitCatalogue) publish(plugin, version string, skills ...string) {
	c.t.Helper()
	if len(skills) == 0 {
		skills = []string{plugin}
	}
	c.plugins[plugin] = gitPlugin{version: version, skills: skills}
	for _, sk := range skills {
		dir := filepath.Join(c.repo, "plugins", plugin, "skills", sk)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			c.t.Fatal(err)
		}
		body := fmt.Sprintf("---\nname: %s\nmetadata:\n  version: %s\ndescription: d\n---\n\n%s at %s\n", sk, version, sk, version)
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			c.t.Fatal(err)
		}
	}
	var versioned, bare []map[string]any
	for name, p := range c.plugins {
		versioned = append(versioned, map[string]any{"name": name, "source": "./plugins/" + name, "version": p.version})
		bare = append(bare, map[string]any{"name": name, "source": "./plugins/" + name})
	}
	for rel, plugins := range map[string][]map[string]any{
		filepath.Join(".agents", "plugins", "marketplace.json"): bare,
		filepath.Join(".claude-plugin", "marketplace.json"):     versioned,
	} {
		data, _ := json.Marshal(map[string]any{"name": "cat", "plugins": plugins})
		path := filepath.Join(c.repo, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			c.t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			c.t.Fatal(err)
		}
	}
	gitCommitAllRepo(c.t, c.repo, !c.inited, "publish "+plugin+" "+version)
	c.inited = true
}

// catalogueHome is a home whose operator file adds the catalogue as the
// marketplace "cat".
func catalogueHome(t *testing.T, c *gitCatalogue) *config.Config {
	t.Helper()
	cfg := marketHome(t)
	if err := config.WriteMarketplacesFile(config.GlobalMarketplacesPath(cfg.Paths.Home), config.MarketplacesFile{
		Marketplaces: []config.DeclaredMarketplace{{Name: "cat", Source: c.url()}},
	}); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestTheVersionOfTheOtherManifestFillsTheGap(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".agents/plugins/marketplace.json", `{"name":"m","plugins":[{"name":"a","source":"./a"},{"name":"b","source":"./b","version":"3.0.0"},{"name":"c","source":"./c"}]}`)
	write(".claude-plugin/marketplace.json", `{"name":"m","plugins":[{"name":"A","source":"./a","version":"1.2.0"},{"name":"b","source":"./b","version":"9.9.9"}]}`)
	mf, path, err := loadMarketplace(root)
	if err != nil || mf == nil || !strings.HasSuffix(path, filepath.Join(".agents", "plugins", "marketplace.json")) {
		t.Fatalf("loadMarketplace = %+v, %q, %v", mf, path, err)
	}
	got := marketplaceVersions(mf)
	want := map[string]string{"a": "1.2.0", "b": "3.0.0"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("versions = %v, want %v: a gap filled, a version of its own kept, c left without one", got, want)
	}
	if mf, path, err := loadMarketplace(t.TempDir()); mf != nil || path != "" || err != nil {
		t.Fatalf("a root without a manifest = %+v, %q, %v", mf, path, err)
	}
}

// An update by name reinstalls nothing that is already the newest version,
// nor a copy the operator raised above it to keep an edit.
func TestAnUpdateByNameKeepsWhatIsCurrentOrNewer(t *testing.T) {
	c := newGitCatalogue(t)
	c.publish("a", "1.0.0")
	cfg := catalogueHome(t, c)
	mustRun(t, cfg, "install", "a@cat")

	out := mustRun(t, cfg, "update", "a")
	if out != "a is up to date." {
		t.Fatalf("update of the newest version answered %q", out)
	}

	// The operator edits the copy and raises its own version, the way the
	// documentation says to keep an edit; the lock still says 1.0.0.
	path := filepath.Join(cfg.Skills.ManagedDir(cfg.Paths.Home), "a", "SKILL.md")
	edited := "---\nname: a\nmetadata:\n  version: 1.5.0\ndescription: mine\n---\n\nmy edit\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	c.publish("a", "1.2.0")
	ups, err := CheckUpdates(context.Background(), cfg, "")
	if err != nil || len(ups) != 1 || ups[0].UpdateAvailable || ups[0].Version != "1.5.0" {
		t.Fatalf("update check = %+v, %v; want the raised copy current", ups, err)
	}
	for _, words := range [][]string{{"update"}, {"update", "a"}} {
		mustRun(t, cfg, words...)
		if body := managedSkillBody(t, cfg, "a"); body != edited {
			t.Fatalf("plugin %s rewrote a newer copy:\n%s", strings.Join(words, " "), body)
		}
	}

	// A release above the edit is an update again.
	c.publish("a", "2.0.0")
	if out := mustRun(t, cfg, "update"); !strings.Contains(out, "0 added, 1 updated, 0 failed.") {
		t.Fatalf("update over 2.0.0 answered %q", out)
	}
	if body := managedSkillBody(t, cfg, "a"); !strings.Contains(body, "a at 2.0.0") {
		t.Fatalf("2.0.0 was not installed: %s", body)
	}
}

// A skill deleted by hand, out of the lock's sight, is not installed: no
// check offers it and no update writes it back, the skill of the delivery
// that an update recorded in the lock included.
func TestAnUpdateDoesNotBringBackASkillDeletedByHand(t *testing.T) {
	c := newGitCatalogue(t)
	c.publish("a", "1.0.0")
	sys := newGitCatalogue(t)
	sys.publish("rpa-feat", "9.0.0")
	cfg := catalogueHome(t, c)
	SystemSources = []config.DeclaredMarketplace{{Name: "rpa-skills", Source: sys.url()}}
	if _, err := SeedDelivery(cfg); err != nil {
		t.Fatal(err)
	}
	managed := cfg.Skills.ManagedDir(cfg.Paths.Home)
	mustRun(t, cfg, "install", "a@cat")
	if out := mustRun(t, cfg, "update", "rpa-feat"); !strings.Contains(out, "1 updated") {
		t.Fatalf("update rpa-feat answered %q", out)
	}
	for _, name := range []string{"a", "rpa-feat"} {
		if err := os.RemoveAll(filepath.Join(managed, name)); err != nil {
			t.Fatal(err)
		}
	}
	c.publish("a", "2.0.0")
	sys.publish("rpa-feat", "10.0.0")

	ups, err := CheckUpdates(context.Background(), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range ups {
		if u.Name == "a" || u.Name == "rpa-feat" {
			t.Fatalf("the update check offers %q, deleted by hand: %+v", u.Name, u)
		}
	}
	mustRun(t, cfg, "update")
	for _, name := range []string{"a", "rpa-feat"} {
		if _, err := os.Stat(filepath.Join(managed, name)); !os.IsNotExist(err) {
			t.Fatalf("%s came back: %v", name, err)
		}
	}
	if _, err := run(t, cfg, "update", "a"); err == nil || !strings.Contains(err.Error(), "is not a skill installed") {
		t.Fatalf("update a by name: %v, want it refused as not installed", err)
	}
}

// A source that cannot be read is a failure on every surface, never "up to
// date": the update errors, the read names it and claims nothing.
func TestAnUnreadableSourceIsNotUpToDate(t *testing.T) {
	c := newGitCatalogue(t)
	c.publish("a", "1.0.0")
	cfg := catalogueHome(t, c)
	mustRun(t, cfg, "install", "a@cat")
	if err := os.RemoveAll(c.repo); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, cfg, "update"); err == nil || !strings.Contains(err.Error(), "update failed") {
		t.Fatalf("update over an unreadable source = %q, %v; want an error", out, err)
	}
	out := mustRun(t, cfg, "marketplace", "update")
	if strings.Contains(out, "up to date") || !strings.Contains(out, "No newer version was found in what could be read.") || !strings.Contains(out, "! cat") {
		t.Fatalf("marketplace update answered %q", out)
	}
}

// A marketplace whose marketplace.json takes the name of the built-in one
// cannot be added under it: <plugin>@rpa-skills would always reach the
// built-in catalogue.
func TestAMarketplaceNamedLikeTheBuiltInOneIsRefused(t *testing.T) {
	fork := newCatalogue(t, config.SystemSkillsMarketplace, "forked")
	reachRemote(t, fork.srv)
	cfg := marketHome(t)
	SystemSources = []config.DeclaredMarketplace{{Name: config.SystemSkillsMarketplace, Source: config.SystemSkillsSource}}
	_, err := run(t, cfg, "marketplace", "add", fork.url())
	if err == nil || !strings.Contains(err.Error(), "the name of the marketplace built into Coddy") || !strings.Contains(err.Error(), "plugin install "+fork.url()) {
		t.Fatalf("adding a fork named rpa-skills: %v", err)
	}
	file, ferr := config.ReadMarketplacesFile(config.GlobalMarketplacesPath(cfg.Paths.Home))
	if ferr != nil || len(file.Marketplaces) != 0 {
		t.Fatalf("the fork was declared: %+v, %v", file, ferr)
	}
}

// A plugin of two skills whose one skill the operator removed: the update
// rewrites the other and does not bring the removed one back.
func TestAnUpdateDoesNotBringBackTheRemovedSkillOfAPlugin(t *testing.T) {
	c := newGitCatalogue(t)
	c.publish("pair", "1.0.0", "left", "right")
	cfg := catalogueHome(t, c)
	if out := mustRun(t, cfg, "install", "pair@cat"); !strings.Contains(out, "2 added") {
		t.Fatalf("install answered %q", out)
	}
	mustRun(t, cfg, "remove", "right")
	c.publish("pair", "2.0.0", "left", "right")

	out := mustRun(t, cfg, "marketplace", "update", "cat")
	if !strings.Contains(out, "1 update available") || !strings.Contains(out, "left 1.0.0 -> 2.0.0") {
		t.Fatalf("marketplace update answered %q", out)
	}
	out = mustRun(t, cfg, "update", "pair")
	if !strings.Contains(out, "0 added, 1 updated, 0 failed.") {
		t.Fatalf("update pair answered %q", out)
	}
	if body := managedSkillBody(t, cfg, "left"); !strings.Contains(body, "left at 2.0.0") {
		t.Fatalf("left was not updated: %s", body)
	}
	if _, err := os.Stat(filepath.Join(cfg.Skills.ManagedDir(cfg.Paths.Home), "right")); !os.IsNotExist(err) {
		t.Fatalf("the removed skill came back: %v", err)
	}
}

// The delivered skills are installed from the system catalogue for updates,
// but only the ones the catalogue publishes, only while they are on disk, and
// never when an install has recorded them already.
func TestDeliveredSkillsCountAsInstalledFromTheCatalogue(t *testing.T) {
	c := newGitCatalogue(t)
	c.publish("rpa-feat", "9.0.0")
	cfg := marketHome(t)
	SystemSources = []config.DeclaredMarketplace{{Name: "rpa-skills", Source: c.url()}}
	if _, err := SeedDelivery(cfg); err != nil {
		t.Fatal(err)
	}
	managed := cfg.Skills.ManagedDir(cfg.Paths.Home)
	if err := os.RemoveAll(filepath.Join(managed, "rpa-init")); err != nil {
		t.Fatal(err)
	}
	entries := installedEntries(managed)
	if ent, ok := entries["rpa-feat"]; !ok || ent.Plugin != "rpa-feat" || ent.Source != c.url() || ent.Version == "" {
		t.Fatalf("rpa-feat = %+v, %v; want it from the catalogue at its delivered version", ent, ok)
	}
	if _, ok := entries["rpa-init"]; ok {
		t.Fatal("a deleted delivered skill counts as installed")
	}

	if _, err := run(t, cfg, "update", "crossreview"); err == nil || !strings.Contains(err.Error(), "a Coddy update brings its new versions") {
		t.Fatalf("updating a delivered skill the catalogue does not publish: %v", err)
	}

	res, err := UpdateSkills(context.Background(), cfg, "", "")
	if err != nil || strings.Join(res.Updated, ",") != "rpa-feat" || len(res.Failed) != 0 {
		t.Fatalf("UpdateSkills = %+v, %v; want rpa-feat updated", res, err)
	}
	if ent := readRemoteLock(managed)["rpa-feat"]; ent.Version != "9.0.0" || ent.Plugin != "rpa-feat" {
		t.Fatalf("lock entry = %+v, want rpa-feat at 9.0.0 from the catalogue", ent)
	}
	if _, err := os.Stat(filepath.Join(managed, "rpa-init")); !os.IsNotExist(err) {
		t.Fatalf("the deleted delivered skill came back: %v", err)
	}
	// The release carries an older copy: the next start leaves the update in place.
	if _, err := SeedDelivery(cfg); err != nil {
		t.Fatal(err)
	}
	if body := managedSkillBody(t, cfg, "rpa-feat"); !strings.Contains(body, "version: 9.0.0") {
		t.Fatalf("the delivery replaced the updated copy: %s", body)
	}
}

// Naming what is not installed from anywhere, or a held project entry, is an
// error; the system catalogue is never written into a file.
func TestRefusalsOfTheReadAndTheUpdate(t *testing.T) {
	cfg := marketHome(t)
	if _, err := UpdateSkills(context.Background(), cfg, "", "nothing"); err == nil || !strings.Contains(err.Error(), "is not a skill installed from a marketplace or a source") {
		t.Fatalf("updating what is not installed: %v", err)
	}

	pcfg, cwd := projectHome(t, "", config.MarketplacesFile{Marketplaces: []config.DeclaredMarketplace{{Name: "team", Source: "owner/team"}}})
	if _, err := RefreshIndex(context.Background(), pcfg, cwd, "team"); !errors.Is(err, ErrHeld) {
		t.Fatalf("reading a held project marketplace: %v, want ErrHeld", err)
	}
	res, err := RefreshIndex(context.Background(), pcfg, cwd, "")
	if err != nil || len(res.Held) != 1 || len(res.Read) != 0 {
		t.Fatalf("reading every list = %+v, %v; want the project marketplace held", res, err)
	}

	c := newCatalogue(t, config.SystemSkillsMarketplace, "logika")
	reachRemote(t, c.srv)
	SystemSources = []config.DeclaredMarketplace{{Name: config.SystemSkillsMarketplace, Source: c.url()}}
	out := mustRun(t, cfg, "marketplace", "add", c.url())
	if !strings.Contains(out, "is already added") {
		t.Fatalf("adding the system catalogue answered %q", out)
	}
	file, err := config.ReadMarketplacesFile(config.GlobalMarketplacesPath(cfg.Paths.Home))
	if err != nil || len(file.Marketplaces)+len(file.Sources) != 0 {
		t.Fatalf("the system catalogue was written into the operator's file: %+v, %v", file, err)
	}
}
