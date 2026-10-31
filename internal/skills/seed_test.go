package skills_test

// Edge cases of the standard delivery. The happy path is
// features/skills_delivery.feature; what is here is everything the delivery
// must refuse to do: clobber a newer copy, hand the same thing over twice, or
// create a config file that was not there - plus the one thing it must do to a
// copy that declares no version.

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

// deliveryHome builds a home for the delivery to land in. withConfig writes a
// config.yaml with an empty source list, which is what an operator who ever
// saved a setting has on disk.
func deliveryHome(t *testing.T, withConfig bool) *config.Config {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := config.Paths{Home: home, CWD: root, ConfigPath: filepath.Join(home, "config.yaml")}
	if !withConfig {
		cfg, err := config.LoadFromCLI(config.CLIPaths{Home: home, CWD: root})
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	if err := os.WriteFile(paths.ConfigPath, []byte("skills:\n  auto_discovery: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// writeSkill puts a skill directory in the managed dir. An empty version writes
// frontmatter without a version: line.
func writeSkill(t *testing.T, cfg *config.Config, name, version, body string) string {
	t.Helper()
	dir := filepath.Join(cfg.Skills.ManagedDir(cfg.Paths.Home), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	head := "---\nname: " + name + "\n"
	if version != "" {
		head += "version: " + version + "\n"
	}
	head += "description: a copy the operator has\n---\n\n" + body + "\n"
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// deliveredVersion is the version the binary carries for name.
func deliveredVersion(t *testing.T, name string) string {
	t.Helper()
	for _, sk := range skills.Bundled() {
		if skills.CanonicalCommandName(sk) == name {
			if sk.Version == "" {
				t.Fatalf("delivered skill %q has no version", name)
			}
			return sk.Version
		}
	}
	t.Fatalf("the delivery has no skill %q", name)
	return ""
}

func TestDeliveryKeepsANewerCopyOnDisk(t *testing.T) {
	cfg := deliveryHome(t, true)
	path := writeSkill(t, cfg, "rpa-feat", "99.0.0", "a copy newer than the release")

	if _, err := skills.SeedDelivery(cfg); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, path); !strings.Contains(got, "a copy newer than the release") {
		t.Fatalf("the delivery overwrote a newer copy:\n%s", got)
	}
}

// A copy with no version: predates the delivery declaring one, so it is older
// than anything the release carries and is replaced. Keeping an edited copy
// means giving it a version higher than the delivered one, same as for any
// other skill of the delivery.
func TestDeliveryReplacesACopyWithoutAVersion(t *testing.T) {
	cfg := deliveryHome(t, true)
	path := writeSkill(t, cfg, "rpa-feat", "", "a copy with no version at all")

	res, err := skills.SeedDelivery(cfg)
	if err != nil {
		t.Fatal(err)
	}

	got := readFile(t, path)
	if strings.Contains(got, "a copy with no version at all") {
		t.Fatalf("the delivery kept a copy that declares no version:\n%s", got)
	}
	if want := "version: " + deliveredVersion(t, "rpa-feat"); !strings.Contains(got, want) {
		t.Fatalf("expected %q on disk, got:\n%s", want, got)
	}
	if len(res.Updated) == 0 {
		t.Fatalf("expected the result to report an update, got %+v", res)
	}
}

func TestDeliveryReplacesAnOlderCopyOnDisk(t *testing.T) {
	cfg := deliveryHome(t, true)
	path := writeSkill(t, cfg, "rpa-feat", "0.0.1", "a copy older than the release")

	res, err := skills.SeedDelivery(cfg)
	if err != nil {
		t.Fatal(err)
	}

	got := readFile(t, path)
	if strings.Contains(got, "a copy older than the release") {
		t.Fatalf("the delivery kept an older copy:\n%s", got)
	}
	if want := "version: " + deliveredVersion(t, "rpa-feat"); !strings.Contains(got, want) {
		t.Fatalf("expected %q on disk, got:\n%s", want, got)
	}
	if len(res.Updated) == 0 {
		t.Fatalf("expected the result to report an update, got %+v", res)
	}
}

func TestDeliveryIsIdempotent(t *testing.T) {
	cfg := deliveryHome(t, true)

	first, err := skills.SeedDelivery(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Installed) == 0 {
		t.Fatal("the first run installed nothing")
	}

	second, err := skills.SeedDelivery(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Installed) != 0 || len(second.Updated) != 0 {
		t.Fatalf("the second run was not a no-op: %+v", second)
	}
}

func TestDeliveryDoesNotCreateAConfigFile(t *testing.T) {
	cfg := deliveryHome(t, false)

	if _, err := skills.SeedDelivery(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.Paths.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("a config file appeared at %s", cfg.Paths.ConfigPath)
	}
	// A home with no config file still has the marketplace: it is a system
	// marketplace, not something the file has to name.
	if keys := inEffectKeys(t, cfg); len(keys) == 0 || keys[0] != config.SystemSkillsMarketplace {
		t.Fatalf("a home without a config file has %v in effect, want the system marketplace", keys)
	}
}

// inEffectKeys names every marketplace and source in effect for the home, in
// order: a marketplace by its name, a source by its address.
func inEffectKeys(t *testing.T, cfg *config.Config) []string {
	t.Helper()
	decls, errs := skills.Declarations(cfg, "")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	var out []string
	for _, d := range decls {
		if d.State == skills.StateReady {
			out = append(out, d.Key())
		}
	}
	return out
}

// declareHome makes <home>/marketplaces.json declare sources.
func declareHome(t *testing.T, cfg *config.Config, sources ...string) {
	t.Helper()
	if err := config.WriteMarketplacesFile(config.GlobalMarketplacesPath(cfg.Paths.Home), config.MarketplacesFile{Sources: sources}); err != nil {
		t.Fatal(err)
	}
}

// The system marketplace is in effect beside the marketplaces.json files, is
// not duplicated when a file happens to name it too (in another spelling
// either), and cannot be taken out of either.
func TestSystemSourceIsListedAndUndeletable(t *testing.T) {
	cfg := deliveryHome(t, true)
	declareHome(t, cfg, "someone/else", "https://github.com/"+config.SystemSkillsSource)

	got := inEffectKeys(t, cfg)
	want := []string{config.SystemSkillsMarketplace, "someone/else"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("in effect: want %v, got %v", want, got)
	}

	for _, key := range []string{config.SystemSkillsSource, config.SystemSkillsMarketplace} {
		if _, err := skills.RemoveSource(cfg, "", key, ""); err == nil {
			t.Fatalf("removing the system marketplace by %q was allowed", key)
		}
	}
	if added, err := skills.AddSource(cfg, "", config.SystemSkillsSource, skills.ScopeGlobal); err != nil || added {
		t.Fatalf("adding the system marketplace should be a no-op, got added=%v err=%v", added, err)
	}
}

func TestDeliveryCarriesEveryBundledSkill(t *testing.T) {
	cfg := deliveryHome(t, true)
	if _, err := skills.SeedDelivery(cfg); err != nil {
		t.Fatal(err)
	}
	managed := cfg.Skills.ManagedDir(cfg.Paths.Home)
	for _, sk := range skills.Bundled() {
		name := skills.CanonicalCommandName(sk)
		if _, err := os.Stat(filepath.Join(managed, name, "SKILL.md")); err != nil {
			t.Errorf("delivered skill %q did not land on disk: %v", name, err)
		}
		if sk.Version == "" {
			t.Errorf("delivered skill %q ships without a version", name)
		}
	}
}

// A SKILL.md Coddy cannot read is not an absent skill and not an unversioned
// one: it is the operator's, and the delivery leaves it whole.
func TestDeliveryKeepsACopyItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires file permissions that deny reads")
	}
	cfg := deliveryHome(t, true)
	path := writeSkill(t, cfg, "rpa-feat", "0.0.1", "a copy behind a closed door")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	res, err := skills.SeedDelivery(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range res.Updated {
		if name == "rpa-feat" {
			t.Fatal("the delivery replaced a skill it could not read")
		}
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); !strings.Contains(got, "a copy behind a closed door") {
		t.Fatalf("the unreadable copy was overwritten:\n%s", got)
	}
}

// A receipt that is there but unreadable says nothing about what was delivered.
// Read as an empty one it would claim nothing ever was, and the run would write
// back every skill the operator had deleted - so the run stops instead.
func TestDeliveryStopsOnAnUnreadableReceipt(t *testing.T) {
	cfg := deliveryHome(t, true)
	if _, err := skills.SeedDelivery(cfg); err != nil {
		t.Fatal(err)
	}
	managed := cfg.Skills.ManagedDir(cfg.Paths.Home)
	if err := os.RemoveAll(filepath.Join(managed, "rpa-feat")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Paths.Home, "skills-delivery.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := skills.SeedDelivery(cfg); err == nil {
		t.Fatal("a corrupt receipt was accepted")
	}
	if _, err := os.Stat(filepath.Join(managed, "rpa-feat", "SKILL.md")); err == nil {
		t.Fatal("the deleted skill was written back on a corrupt receipt")
	}
}

// Replacing a skill renames the old copy aside and the new one into place. A
// process killed between the two leaves a backup and no skill; the next run
// puts it back rather than reading the gap as a deletion.
func TestDeliveryRecoversAnInterruptedReplacement(t *testing.T) {
	cfg := deliveryHome(t, true)
	if _, err := skills.SeedDelivery(cfg); err != nil {
		t.Fatal(err)
	}
	managed := cfg.Skills.ManagedDir(cfg.Paths.Home)

	// What a kill mid-swap leaves: the skill renamed to a pid-tagged backup.
	live := filepath.Join(managed, "rpa-feat")
	if err := os.Rename(live, filepath.Join(managed, ".bak-rpa-feat-4242")); err != nil {
		t.Fatal(err)
	}

	if _, err := skills.SeedDelivery(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(live, "SKILL.md")); err != nil {
		t.Fatalf("the interrupted replacement was not recovered: %v", err)
	}
	if _, err := os.Stat(filepath.Join(managed, ".bak-rpa-feat-4242")); err == nil {
		t.Fatal("the backup was left behind after recovery")
	}
}

// A marketplaces.json that also names the system marketplace carries a
// redundant entry. Removing it takes the entry out of the file and says the
// marketplace stays.
func TestRemovingARedundantConfigEntryClearsTheFile(t *testing.T) {
	cfg := deliveryHome(t, true)
	declareHome(t, cfg, "someone/else", config.SystemSkillsSource)

	_, err := skills.RemoveSource(cfg, "", config.SystemSkillsSource, "")
	if err == nil || !strings.Contains(err.Error(), "stays in effect") {
		t.Fatalf("expected the answer to say the marketplace stays, got %v", err)
	}
	file, err := config.ReadMarketplacesFile(config.GlobalMarketplacesPath(cfg.Paths.Home))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range file.Sources {
		if strings.EqualFold(s, config.SystemSkillsSource) {
			t.Fatalf("the redundant entry is still in marketplaces.json: %v", file.Sources)
		}
	}
	// The marketplace is in effect all the same, and the other source survived.
	if keys := inEffectKeys(t, cfg); !reflect.DeepEqual(keys, []string{config.SystemSkillsMarketplace, "someone/else"}) {
		t.Fatalf("in effect after the removal: %v", keys)
	}
}

// The record of what was handed over lives in the coddy home, beside the
// skills folder rather than in it: deleting the folder is not a first install.
// A record an older Coddy kept inside the folder moves out on the next run,
// with what it said.
func TestTheDeliveryRecordLivesBesideTheSkillsFolder(t *testing.T) {
	cfg := deliveryHome(t, true)
	if _, err := skills.SeedDelivery(cfg); err != nil {
		t.Fatal(err)
	}
	managed := cfg.Skills.ManagedDir(cfg.Paths.Home)
	record := filepath.Join(cfg.Paths.Home, "skills-delivery.json")
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("no delivery record in the home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(managed, ".bundled.json")); !os.IsNotExist(err) {
		t.Fatalf("a record inside the skills folder: %v", err)
	}

	// An older Coddy's record inside the folder: rpa-feat handed over and
	// deleted since.
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(managed, "rpa-feat")); err != nil {
		t.Fatal(err)
	}
	legacy := `{"version":1,"skills":{"rpa-feat":"1.0.0"}}`
	if err := os.WriteFile(filepath.Join(managed, ".bundled.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := skills.SeedDelivery(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(managed, "rpa-feat")); !os.IsNotExist(err) {
		t.Fatalf("the deleted skill came back on the move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(managed, ".bundled.json")); !os.IsNotExist(err) {
		t.Fatalf("the old record stayed in the skills folder: %v", err)
	}
	data, err := os.ReadFile(record)
	if err != nil || !strings.Contains(string(data), `"rpa-feat"`) {
		t.Fatalf("the moved record = %s, %v", data, err)
	}

	// The whole folder deleted: nothing comes back.
	if err := os.RemoveAll(managed); err != nil {
		t.Fatal(err)
	}
	if _, err := skills.SeedDelivery(cfg); err != nil {
		t.Fatal(err)
	}
	for _, sk := range skills.Bundled() {
		if _, err := os.Stat(filepath.Join(managed, skills.CanonicalCommandName(sk))); !os.IsNotExist(err) {
			t.Fatalf("%s came back after the skills folder was deleted: %v", skills.CanonicalCommandName(sk), err)
		}
	}
}
