package rules_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/rules"
)

func ruleNames(rs []*rules.Rule) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.CanonicalName())
	}
	sort.Strings(out)
	return out
}

// A rule kept in a folder of a rule folder is named by its path there, so
// files of one name in different folders are all read rather than the first.
func TestRuleNamesCarryTheirFolderInsideTheRuleFolder(t *testing.T) {
	root := t.TempDir()
	writeRuleTree(t, root, ".coddy/rules/top.mdc", ".coddy/rules/frontend/style.mdc", ".coddy/rules/backend/style.mdc", ".coddy/rules/backend/api/style.mdc")
	got, err := rules.DefaultFactory("").Discover(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := ruleNames(got)
	want := []string{"backend/api/style", "backend/style", "frontend/style", "top"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
}

// A project rule wins over the operator's rule at the same path of its rule
// folder, and only there: a rule of the same file name in another folder is
// another rule.
func TestProjectRuleWinsOnlyAtTheSamePath(t *testing.T) {
	home := t.TempDir()
	writeRuleTree(t, home, "rules/style.mdc", "rules/frontend/lint.mdc")
	root := t.TempDir()
	writeRuleTree(t, root, ".coddy/rules/frontend/style.mdc", ".coddy/rules/frontend/lint.mdc")
	got, err := rules.DefaultFactory(home).Discover(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := ruleNames(got)
	if len(names) != 3 || names[0] != "frontend/lint" || names[1] != "frontend/style" || names[2] != "style" {
		t.Fatalf("names = %v, want [frontend/lint frontend/style style]", names)
	}
	for _, r := range got {
		if r.CanonicalName() == "frontend/lint" && r.Source != rules.SourceCoddy {
			t.Fatalf("frontend/lint came from %s, want the project's", r.Source)
		}
	}
}

// A mirror for another agent is matched by the path inside the rule folder:
// the Claude Code copy of frontend/x is a mirror, its backend/y is a rule the
// session goes without.
func TestMirrorIsMatchedByThePathInsideTheRuleFolder(t *testing.T) {
	root := t.TempDir()
	writeRuleTree(t, root, ".cursor/rules/frontend/x.mdc", ".claude/rules/frontend/x.md", ".claude/rules/backend/y.md")
	d, err := rules.DefaultFactory("").Inspect(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.OnlySkipped) != 1 || d.OnlySkipped[0] != ".claude/rules/backend/y.md" {
		t.Fatalf("only in a folder not read = %v, want [.claude/rules/backend/y.md]", d.OnlySkipped)
	}
}

// The rule folder of a subfolder is read the same way: its rules are named by
// their path inside it.
func TestNestedRuleNamesCarryTheirFolderInsideTheRuleFolder(t *testing.T) {
	root := t.TempDir()
	writeRuleTree(t, root, "db/.coddy/rules/deploy.mdc", "db/.coddy/rules/ops/deploy.mdc")
	if err := os.MkdirAll(filepath.Join(root, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	names := ruleNames(rules.DefaultFactory("").Folder(root, filepath.Join(root, "db"), nil))
	if len(names) != 2 || names[0] != "deploy" || names[1] != "ops/deploy" {
		t.Fatalf("rules of db = %v, want [deploy ops/deploy]", names)
	}
}
