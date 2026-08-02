package skills_test

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

func TestBundledIncludesSystemSkills(t *testing.T) {
	b := skills.Bundled()
	if len(b) != 2 {
		t.Fatalf("expected 2 bundled skills, got %d", len(b))
	}
	found := make(map[string]bool, len(b))
	for _, skill := range b {
		found[skills.CanonicalCommandName(skill)] = true
	}
	for _, name := range []string{"generate-rules", "configure-coddy"} {
		if !found[name] {
			t.Fatalf("bundled skill %q missing from %+v", name, found)
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
	if !found["generate-rules"] || !found["configure-coddy"] {
		t.Fatalf("bundled skills missing from LoadAll: %+v", found)
	}
}
