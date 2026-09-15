package skills_test

// Godog harness for features/skills_delivery.feature: hands the standard
// delivery to a temporary home and reads back what landed there, what the
// catalogue offers, and which marketplaces the config ended up naming.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

type deliveryState struct {
	root string
	home string
	cfg  *config.Config
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
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
	s.cfg = nil
}

// emptyHome builds a home with a config file in it, which is what an operator
// who has ever saved a setting has; the delivery registers its marketplace in
// that file rather than creating one.
func (s *deliveryState) emptyHome() error {
	s.home = filepath.Join(s.root, "home")
	if err := os.MkdirAll(s.home, 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(s.home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("skills:\n  sources: []\n"), 0o644); err != nil {
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

func (s *deliveryState) sourcesContain(source string) error {
	for _, got := range skills.ListSources(s.cfg) {
		if strings.EqualFold(got, source) {
			return nil
		}
	}
	return fmt.Errorf("sources %v do not name %q", s.cfg.Skills.Sources, source)
}

func (s *deliveryState) sourcesDoNotContain(source string) error {
	if err := s.sourcesContain(source); err == nil {
		return fmt.Errorf("sources still name %q", source)
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

func (s *deliveryState) removeMarketplace(source string) error {
	removed, err := skills.RemoveSource(s.cfg, source)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("marketplace %q was not configured", source)
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
	sc.Step(`^the skill catalogue offers "([^"]*)"$`, s.catalogueOffers)
	sc.Step(`^the configured skill sources contain "([^"]*)"$`, s.sourcesContain)
	sc.Step(`^the configured skill sources do not contain "([^"]*)"$`, s.sourcesDoNotContain)
	sc.Step(`^the home already carries skill "([^"]*)" at version "([^"]*)"$`, s.writeSkill)
	sc.Step(`^the home skill "([^"]*)" is at the delivered version$`, s.atDeliveredVersion)
	sc.Step(`^the operator deletes the skill "([^"]*)"$`, s.deleteSkill)
	sc.Step(`^the operator removes the marketplace "([^"]*)"$`, s.removeMarketplace)
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
