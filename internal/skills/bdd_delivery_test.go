package skills_test

// Godog harness for features/skills_delivery.feature: hands the standard
// delivery to a temporary home and reads back what landed there, what the
// catalogue offers, and which marketplaces the config ended up naming. The
// built-in catalogue is a git repository on disk when a scenario publishes
// into it, so nothing reaches GitHub.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

type deliveryState struct {
	root    string
	home    string
	cfg     *config.Config
	cfgFile []byte // config.yaml as written, to prove the delivery leaves it alone
	answer  string // what the last plugin command printed

	// catalogue is the git repository standing in for the built-in
	// catalogue, and published what it publishes, by plugin name.
	catalogue string
	published map[string]string
	restore   func()
}

func (s *deliveryState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-delivery-*")
	if err != nil {
		return err
	}
	s.root = root
	return nil
}

func (s *deliveryState) close() {
	if s.restore != nil {
		s.restore()
		s.restore = nil
	}
	s.catalogue, s.published, s.answer = "", nil, ""
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
	s.cfg = nil
}

// emptyHome builds a home with a config file in it, which is what an operator
// who has ever saved a setting has. The delivery never writes to it: its
// marketplace is built into Coddy, not configured.
func (s *deliveryState) emptyHome() error {
	s.home = filepath.Join(s.root, "home")
	if err := os.MkdirAll(s.home, 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(s.home, "config.yaml")
	s.cfgFile = []byte("skills:\n  auto_discovery: true\n")
	if err := os.WriteFile(cfgPath, s.cfgFile, 0o644); err != nil {
		return err
	}
	cfg, err := config.LoadWithPaths(config.Paths{Home: s.home, ConfigPath: cfgPath, CWD: s.root})
	if err != nil {
		return err
	}
	s.cfg = cfg
	return nil
}

func (s *deliveryState) managedDir() string { return filepath.Join(s.home, "skills") }

func (s *deliveryState) hand() error {
	_, err := skills.SeedDelivery(s.cfg)
	return err
}

func (s *deliveryState) carries(name string) error {
	if _, err := os.Stat(filepath.Join(s.managedDir(), name, "SKILL.md")); err != nil {
		return fmt.Errorf("skill %q is not in %s: %w", name, s.managedDir(), err)
	}
	return nil
}

func (s *deliveryState) doesNotCarry(name string) error {
	if _, err := os.Stat(filepath.Join(s.managedDir(), name)); err == nil {
		return fmt.Errorf("skill %q is back in %s", name, s.managedDir())
	}
	return nil
}

func (s *deliveryState) carriesReferences(name string) error {
	dir := filepath.Join(s.managedDir(), name, "references")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("skill %q has no references on disk: %w", name, err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("skill %q has an empty references directory", name)
	}
	return nil
}

// carriesScripts: a skill that ships a helper (crossreview's scripts/) has it
// written next to its SKILL.md, executable where modes exist, because the
// skill tells the model to run it from there.
func (s *deliveryState) carriesScripts(name string) error {
	dir := filepath.Join(s.managedDir(), name, "scripts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("skill %q has no scripts on disk: %w", name, err)
	}
	runnable := 0
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if ext != ".py" && ext != ".sh" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
			return fmt.Errorf("skill %q: %s is not executable (%v)", name, e.Name(), info.Mode())
		}
		runnable++
	}
	if runnable == 0 {
		return fmt.Errorf("skill %q has no runnable script in %s", name, dir)
	}
	return nil
}

func (s *deliveryState) catalogueOffers(name string) error {
	loader := skills.NewLoader(s.cfg.Skills.Dirs)
	loaded, err := loader.LoadAll(s.root, s.home, s.managedDir())
	if err != nil {
		return err
	}
	for _, sum := range skills.ListSkills(loaded) {
		if sum.Name == name {
			return nil
		}
	}
	return fmt.Errorf("skill %q is not in the catalogue", name)
}

func (s *deliveryState) catalogueDoesNotOffer(name string) error {
	if err := s.catalogueOffers(name); err == nil {
		return fmt.Errorf("the catalogue still offers %q", name)
	}
	return nil
}

func (s *deliveryState) configUntouched() error {
	got, err := os.ReadFile(s.cfg.Paths.ConfigPath)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, s.cfgFile) {
		return fmt.Errorf("the delivery rewrote the config file:\n%s", got)
	}
	return nil
}

func (s *deliveryState) writeSkill(name, version string) error {
	dir := filepath.Join(s.managedDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("---\nname: %s\nversion: %s\ndescription: an older copy\n---\n\nold body\n", name, version)
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644)
}

func (s *deliveryState) atDeliveredVersion(name string) error {
	want := ""
	for _, sk := range skills.Bundled() {
		if skills.CanonicalCommandName(sk) == name {
			want = sk.Version
		}
	}
	if want == "" {
		return fmt.Errorf("the delivery has no versioned skill %q", name)
	}
	data, err := os.ReadFile(filepath.Join(s.managedDir(), name, "SKILL.md"))
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), "version: "+want) {
		return fmt.Errorf("skill %q on disk is not at the delivered version %q", name, want)
	}
	return nil
}

func (s *deliveryState) deleteSkill(name string) error {
	return os.RemoveAll(filepath.Join(s.managedDir(), name))
}

func (s *deliveryState) deleteSkillsDir() error {
	return os.RemoveAll(s.managedDir())
}

func (s *deliveryState) removeMarketplaceRefused(source string) error {
	removed, err := skills.RemoveSource(s.cfg, "", source, "")
	if err == nil {
		return fmt.Errorf("removing %q was allowed (removed=%v)", source, removed)
	}
	if removed {
		return fmt.Errorf("removing %q failed with %v but reported a removal", source, err)
	}
	return nil
}

// builtInMarketplace: the system catalogue is listed under its name, reading
// from its address, built in and trusted, without any file declaring it.
func (s *deliveryState) builtInMarketplace(name, source string) error {
	decls, errs := skills.Declarations(s.cfg, "")
	if len(errs) > 0 {
		return errs[0]
	}
	for _, d := range decls {
		if d.Origin != skills.OriginSystem {
			continue
		}
		if d.Kind == skills.KindMarketplace && d.Name == name && strings.EqualFold(d.Source, source) && d.Trusted && d.Path == "" {
			return nil
		}
		return fmt.Errorf("the built-in entry is %+v, want the marketplace %q reading from %q", d, name, source)
	}
	return fmt.Errorf("no built-in marketplace among %+v", decls)
}

// cataloguePublishes puts a plugin at version into the stand-in for the
// built-in catalogue: a git repository whose .agents manifest names no
// versions and whose .claude-plugin manifest does, the way the real
// rpa-skills publishes both.
func (s *deliveryState) cataloguePublishes(plugin, version string) error {
	if s.catalogue == "" {
		s.catalogue = filepath.Join(s.root, "rpa-skills")
		s.published = map[string]string{}
		if err := os.MkdirAll(s.catalogue, 0o755); err != nil {
			return err
		}
		if err := s.git("init", "-q", "-b", "main"); err != nil {
			return err
		}
		prev := skills.SystemSources
		skills.SystemSources = []config.DeclaredMarketplace{{Name: "rpa-skills", Source: "file://" + filepath.ToSlash(s.catalogue)}}
		s.restore = func() { skills.SystemSources = prev }
	}
	s.published[plugin] = version
	dir := filepath.Join(s.catalogue, "plugins", plugin)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("---\nname: %s\nmetadata:\n  version: %s\ndescription: %s from the catalogue\n---\n\n# %s\n", plugin, version, plugin, plugin)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		return err
	}
	var versioned, bare []map[string]any
	for name, v := range s.published {
		versioned = append(versioned, map[string]any{"name": name, "source": "./plugins/" + name, "version": v})
		bare = append(bare, map[string]any{"name": name, "source": "./plugins/" + name})
	}
	for rel, plugins := range map[string][]map[string]any{
		filepath.Join(".agents", "plugins", "marketplace.json"): bare,
		filepath.Join(".claude-plugin", "marketplace.json"):     versioned,
	} {
		data, err := json.MarshalIndent(map[string]any{"name": "rpa-skills", "plugins": plugins}, "", "  ")
		if err != nil {
			return err
		}
		path := filepath.Join(s.catalogue, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	if err := s.git("add", "-A"); err != nil {
		return err
	}
	return s.git("commit", "-q", "-m", "publish "+plugin+" "+version)
}

func (s *deliveryState) git(args ...string) error {
	cmd := exec.Command("git", append([]string{"-c", "user.email=c@t", "-c", "user.name=c"}, args...)...)
	cmd.Dir = s.catalogue
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %w\n%s", args, err, out)
	}
	return nil
}

func (s *deliveryState) runPlugin(command string) error {
	out, err := skills.RunPluginCommand(context.Background(), s.cfg, s.root, strings.Fields(command))
	if err != nil {
		return fmt.Errorf("plugin %s: %w", command, err)
	}
	s.answer = out
	return nil
}

func (s *deliveryState) answers(want string) error {
	if !strings.Contains(s.answer, want) {
		return fmt.Errorf("the plugin command answered %q, want it to say %q", s.answer, want)
	}
	return nil
}

func (s *deliveryState) atVersion(name, version string) error {
	data, err := os.ReadFile(filepath.Join(s.managedDir(), name, "SKILL.md"))
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), "version: "+version) {
		return fmt.Errorf("skill %q on disk is not at version %q:\n%s", name, version, data)
	}
	return nil
}

func initializeDeliveryScenario(sc *godog.ScenarioContext) {
	s := &deliveryState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^an empty coddy home$`, s.emptyHome)
	sc.Step(`^coddy hands over the standard delivery$`, s.hand)
	sc.Step(`^coddy has handed over the standard delivery$`, s.hand)
	sc.Step(`^the home skills directory carries "([^"]*)"$`, s.carries)
	sc.Step(`^the home skills directory does not carry "([^"]*)"$`, s.doesNotCarry)
	sc.Step(`^the skill "([^"]*)" carries its references on disk$`, s.carriesReferences)
	sc.Step(`^the skill "([^"]*)" carries its runnable scripts on disk$`, s.carriesScripts)
	sc.Step(`^the skill catalogue offers "([^"]*)"$`, s.catalogueOffers)
	sc.Step(`^the skill catalogue does not offer "([^"]*)"$`, s.catalogueDoesNotOffer)
	sc.Step(`^the built-in marketplace "([^"]*)" reads from "([^"]*)"$`, s.builtInMarketplace)
	sc.Step(`^the built-in catalogue publishes "([^"]*)" at version "([^"]*)"$`, s.cataloguePublishes)
	sc.Step(`^I run the plugin command "([^"]*)"$`, s.runPlugin)
	sc.Step(`^the plugin command answers "([^"]*)"$`, s.answers)
	sc.Step(`^the home skill "([^"]*)" is at version "([^"]*)"$`, s.atVersion)
	sc.Step(`^removing the marketplace "([^"]*)" is refused$`, s.removeMarketplaceRefused)
	sc.Step(`^the config file was not touched$`, s.configUntouched)
	sc.Step(`^the home already carries skill "([^"]*)" at version "([^"]*)"$`, s.writeSkill)
	sc.Step(`^the home skill "([^"]*)" is at the delivered version$`, s.atDeliveredVersion)
	sc.Step(`^the operator deletes the skill "([^"]*)"$`, s.deleteSkill)
	sc.Step(`^the operator deletes the home skills directory$`, s.deleteSkillsDir)
}

func TestSkillsDeliveryFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "skills-delivery",
		ScenarioInitializer: initializeDeliveryScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/skills_delivery.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("skills delivery feature failed")
	}
}
