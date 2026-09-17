package skills_test

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

// The standard delivery: what the binary carries and hands to a home. The
// system skill configure-coddy is written here; the rpa-* workflow skills are
// vendored from their own repositories by scripts/vendor-bundled-skills.sh.
var deliveredSkills = []string{
	"configure-coddy",
	"rpa-bugfix",
	"rpa-feat",
	"rpa-gen-rules",
	"rpa-init",
}

func TestBundledCarriesTheDelivery(t *testing.T) {
	b := skills.Bundled()
	found := make(map[string]bool, len(b))
	for _, skill := range b {
		found[skills.CanonicalCommandName(skill)] = true
	}
	for _, name := range deliveredSkills {
		if !found[name] {
			t.Errorf("delivered skill %q missing from %+v", name, found)
		}
	}
	if len(b) != len(deliveredSkills) {
		t.Errorf("expected %d delivered skills, got %d: %+v", len(deliveredSkills), len(b), found)
	}
}

// Every delivered skill declares a version: it is what decides whether a
// release replaces the copy in an operator's home.
func TestBundledSkillsDeclareAVersion(t *testing.T) {
	for _, e := range skills.BundledEntries() {
		if e.Version == "" {
			t.Errorf("delivered skill %q has no version in its SKILL.md frontmatter", e.Name)
		}
	}
}

func TestLoadAllPrependsBundled(t *testing.T) {
	loader := skills.NewLoader(nil)
	all, err := loader.LoadAll(".", "")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, s := range all {
		found[skills.CanonicalCommandName(s)] = true
	}
	for _, name := range deliveredSkills {
		if !found[name] {
			t.Fatalf("delivered skill %q missing from LoadAll: %+v", name, found)
		}
	}
}
