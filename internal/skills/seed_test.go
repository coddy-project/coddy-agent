package skills_test

// Edge cases of the standard delivery. The happy path is
// features/skills_delivery.feature; what is here is everything the delivery
// must refuse to do: clobber a newer copy, clobber a copy it cannot compare
// itself against, hand the same thing over twice, or create a config file that
// was not there.

import (
	"os"
	"path/filepath"
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
	if err := os.WriteFile(paths.ConfigPath, []byte("skills:\n  sources: []\n"), 0o644); err != nil {
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

func TestDeliveryKeepsACopyItCannotCompare(t *testing.T) {
	cfg := deliveryHome(t, true)
	path := writeSkill(t, cfg, "rpa-feat", "", "a copy with no version at all")

	if _, err := skills.SeedDelivery(cfg); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, path); !strings.Contains(got, "a copy with no version at all") {
		t.Fatalf("the delivery overwrote a copy it cannot compare itself against:\n%s", got)
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
	if len(second.Installed) != 0 || len(second.Updated) != 0 || second.SourceAdded {
		t.Fatalf("the second run was not a no-op: %+v", second)
	}
}

func TestDeliveryDoesNotCreateAConfigFile(t *testing.T) {
	cfg := deliveryHome(t, false)

	res, err := skills.SeedDelivery(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.SourceAdded {
		t.Fatal("the delivery wrote a marketplace into a config file that does not exist")
	}
	if _, err := os.Stat(cfg.Paths.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("a config file appeared at %s", cfg.Paths.ConfigPath)
	}
	// Without a file the default still connects the marketplace in memory.
	if len(skills.ListSources(cfg)) == 0 {
		t.Fatal("a home without a config file has no marketplace configured")
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
