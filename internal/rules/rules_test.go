package rules_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/rules"
)

func TestMatchAutoStickyGlob(t *testing.T) {
	r := &rules.Rule{
		ID:          "coddy:/tmp/go.mdc",
		Name:        "go-standards",
		AlwaysApply: true,
		ApplyMode:   rules.ApplyAuto,
		Globs:       []string{"**/*.go"},
		Content:     "RULE_GLOB_TOKEN",
	}
	catalog := []*rules.Rule{r}
	matched := rules.MatchAuto(catalog, []string{"/proj/main.go"})
	if len(matched) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matched))
	}
	sticky := rules.UnionStable(nil, matched)
	if len(sticky) != 1 {
		t.Fatalf("sticky len %d", len(sticky))
	}
	// No glob match this turn — still sticky.
	sticky2 := rules.UnionStable(sticky, rules.MatchAuto(catalog, nil))
	if len(sticky2) != 1 {
		t.Fatalf("expected sticky to remain, got %d", len(sticky2))
	}
}

func TestMentionOnlyNoAuto(t *testing.T) {
	r := &rules.Rule{
		ID:          "coddy:/tmp/manual.mdc",
		Name:        "manual-rule",
		AlwaysApply: false,
		ApplyMode:   rules.ApplyMention,
		Globs:       []string{"**/*.go"},
		Content:     "SECRET_MENTION",
	}
	catalog := []*rules.Rule{r}
	if len(rules.MatchAuto(catalog, []string{"/x.go"})) != 0 {
		t.Fatal("mention rule must not auto-match")
	}
	if len(rules.SelectMentioned(catalog, "hello")) != 0 {
		t.Fatal("no mention")
	}
	got := rules.SelectMentioned(catalog, "see @manual-rule please")
	if len(got) != 1 || !strings.Contains(got[0].Content, "SECRET") {
		t.Fatalf("mention: %+v", got)
	}
}

func TestRenderPromptDedupe(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte("# Agents\n\nAlways read tests."), 0o644); err != nil {
		t.Fatal(err)
	}
	auto := &rules.Rule{ID: "a:1", Name: "a", AlwaysApply: true, ApplyMode: rules.ApplyAuto, Content: "auto body"}
	mention := &rules.Rule{ID: "b:2", Name: "b", AlwaysApply: false, ApplyMode: rules.ApplyMention, Content: "mention body"}
	out := rules.RenderPrompt(tmp, []*rules.Rule{auto}, []*rules.Rule{auto, mention})
	if !strings.Contains(out, "AGENTS.md") {
		t.Fatal("missing agents")
	}
	if strings.Count(out, "auto body") != 1 {
		t.Fatal("dedupe failed for auto")
	}
	if !strings.Contains(out, "mention body") {
		t.Fatal("missing mention")
	}
}

func TestDiscoverPrecedence(t *testing.T) {
	tmp := t.TempDir()
	for _, sub := range []string{".cursor/rules", ".coddy/rules"} {
		if err := os.MkdirAll(filepath.Join(tmp, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(tmp, ".cursor/rules", "dup.mdc"), []byte("---\nalwaysApply: true\nglobs: ['**/*']\n---\nfrom cursor"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, ".coddy/rules", "dup.mdc"), []byte("---\nalwaysApply: true\nglobs: ['**/*']\n---\nfrom coddy"), 0o644)
	got, err := rules.DefaultFactory().Discover(tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("dedupe expected 1, got %d", len(got))
	}
	if !strings.Contains(got[0].Content, "from coddy") {
		t.Fatalf("want coddy win, got %q", got[0].Content)
	}
}

func TestParseAtMentions(t *testing.T) {
	names := rules.ParseAtMentions("Use @foo in text")
	if len(names) != 1 || names[0] != "foo" {
		t.Fatalf("got %v", names)
	}
}
