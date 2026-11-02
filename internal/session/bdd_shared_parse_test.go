package session_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

func TestSharedInstructionParseFeature(t *testing.T) {
	root := t.TempDir()
	rulePath := filepath.Join(root, ".coddy", "rules", "style.md")
	skillPath := filepath.Join(root, ".coddy", "skills", "review", "SKILL.md")
	body := strings.Repeat("Read the diff first.\n", 100)
	cfg := &config.Config{}
	loader := skills.NewLoader([]string{filepath.Join(root, ".coddy", "skills")})
	var ruleCatalog []*rules.Rule
	var skillCatalog []*skills.Skill

	write := func(text string) error {
		for _, path := range []string{rulePath, skillPath} {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	load := func() (*rules.Rule, *skills.Skill, error) {
		rr := session.DiscoverRules(cfg, root)
		if len(rr) != 1 {
			return nil, nil, fmt.Errorf("got %d rules, want one", len(rr))
		}
		ss, err := loader.LoadAll(root, "")
		if err != nil {
			return nil, nil, err
		}
		for _, skill := range ss {
			if skill.FilePath == skillPath {
				return rr[0], skill, nil
			}
		}
		return nil, nil, fmt.Errorf("workspace skill not found")
	}
	suite := godog.TestSuite{
		Name: "shared-instruction-parse",
		Options: &godog.Options{
			Format: "progress", Paths: []string{"../../features/shared_instruction_parse.feature"}, TestingT: t,
		},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a workspace with a rule and a skill$`, func() error { return write(body) })
			sc.Step(`^instruction catalogs are loaded for (\d+) sessions$`, func(n int) error {
				for range n {
					r, s, err := load()
					if err != nil {
						return err
					}
					ruleCatalog = append(ruleCatalog, r)
					skillCatalog = append(skillCatalog, s)
				}
				return nil
			})
			sc.Step(`^the catalogs own separate objects sharing the unchanged text$`, func() error {
				for i := 1; i < len(ruleCatalog); i++ {
					if ruleCatalog[i] == ruleCatalog[0] || skillCatalog[i] == skillCatalog[0] {
						return fmt.Errorf("catalog %d shares mutable objects", i)
					}
					if unsafe.StringData(ruleCatalog[i].Content) != unsafe.StringData(ruleCatalog[0].Content) ||
						unsafe.StringData(skillCatalog[i].Content) != unsafe.StringData(skillCatalog[0].Content) {
						return fmt.Errorf("catalog %d retains another allocation of instruction text", i)
					}
				}
				return nil
			})
			sc.Step(`^the rule and skill files are edited$`, func() error { return write("Run tests.\n") })
			sc.Step(`^a fresh catalog sees the edits while earlier catalogs keep their text$`, func() error {
				r, s, err := load()
				if err != nil {
					return err
				}
				if r.Content != "Run tests." || s.Content != "Run tests." ||
					ruleCatalog[0].Content != strings.TrimSpace(body) || skillCatalog[0].Content != strings.TrimSpace(body) {
					return fmt.Errorf("edited files were not reread or earlier catalogs were mutated")
				}
				return nil
			})
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("feature suite exited %d", status)
	}
}
