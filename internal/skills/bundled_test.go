package skills_test

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

func TestBundledIncludesGenerateRules(t *testing.T) {
	b := skills.Bundled()
	if len(b) != 1 {
		t.Fatalf("expected 1 bundled skill, got %d", len(b))
	}
	if skills.CanonicalCommandName(b[0]) != "coddy-generate-rules" {
		t.Fatalf("name %q", skills.CanonicalCommandName(b[0]))
	}
}

func TestLoadAllPrependsBundled(t *testing.T) {
	loader := skills.NewLoader(nil)
	all, err := loader.LoadAll(".", "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range all {
		if skills.CanonicalCommandName(s) == "coddy-generate-rules" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("bundled skill missing from LoadAll")
	}
}
