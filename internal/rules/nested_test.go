package rules_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/rules"
)

// nestedGroup is a folder that holds several projects, each with a rule folder
// of its own (issue #557): a group rule at the root, a subproject that mirrors
// its Cursor rule for Claude Code, a subproject with a glob rule and a folder
// inside it with rules again, dependency trees that carry rules too, and a
// hidden folder (a worktree under .coddy/worktrees) with a copy of the group's.
func nestedGroup(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	writeRuleTree(t, tmp,
		".cursor/rules/group.mdc",
		"mobile/.cursor/rules/mobile.mdc",
		"mobile/.claude/rules/mobile.md",
		"db/sub/.cursor/rules/deep.mdc",
		"node_modules/pkg/.cursor/rules/dep.mdc",
		"vendor/lib/.cursor/rules/lib.mdc",
		".coddy/worktrees/feat/.cursor/rules/group.mdc",
	)
	path := filepath.Join(tmp, "db", ".coddy", "rules", "db.mdc")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nglobs: migrations/**\n---\nmigrations"), 0o644); err != nil {
		t.Fatal(err)
	}
	return tmp
}

func ruleFiles(root string, rs []*rules.Rule) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		rel, _ := filepath.Rel(root, r.FilePath)
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

func TestNestedReadsTheRuleFolderOfEveryFolderOnTheWay(t *testing.T) {
	tmp := nestedGroup(t)
	f := rules.DefaultFactory("")

	got := f.Nested(tmp, []string{filepath.Join(tmp, "db", "sub", "x.sql")}, nil)
	if files := ruleFiles(tmp, got); len(files) != 2 || files[0] != "db/.coddy/rules/db.mdc" || files[1] != "db/sub/.cursor/rules/deep.mdc" {
		t.Fatalf("rules for db/sub/x.sql = %v, want db's, then db/sub's", files)
	}
	for _, r := range got {
		dir := filepath.Dir(filepath.Dir(filepath.Dir(r.FilePath)))
		if r.Root != dir || r.ScopeDir != dir {
			t.Fatalf("%s: Root=%q ScopeDir=%q, want both %q", r.FilePath, r.Root, r.ScopeDir, dir)
		}
		if r.AlwaysOn() {
			t.Fatalf("%s is always on: a nested rule must never reach the system prompt", r.FilePath)
		}
	}

	// The first folder of the chain that holds a rule file wins, as at the
	// root: the Claude Code mirror of mobile's rule is not read beside it.
	if files := ruleFiles(tmp, f.Nested(tmp, []string{"mobile/app.ts"}, nil)); len(files) != 1 || files[0] != "mobile/.cursor/rules/mobile.mdc" {
		t.Fatalf("rules for mobile/app.ts = %v, want the Cursor rule only", files)
	}
	// rules.systems narrows the chain of a subfolder as it does the root's.
	if files := ruleFiles(tmp, f.Nested(tmp, []string{"mobile/app.ts"}, []rules.Source{rules.SourceClaude})); len(files) != 1 || files[0] != "mobile/.claude/rules/mobile.md" {
		t.Fatalf("rules for mobile/app.ts under systems [claude] = %v", files)
	}
	// A folder reached by several paths is read once.
	if n := len(f.Nested(tmp, []string{"mobile/a.ts", "mobile/b.ts"}, nil)); n != 1 {
		t.Fatalf("two paths in one folder read its rules %d times", n)
	}
}

func TestNestedSkipsWhatDiscoveryNeverEnters(t *testing.T) {
	tmp := nestedGroup(t)
	f := rules.DefaultFactory("")
	for _, p := range []string{
		filepath.Join(tmp, "node_modules", "pkg", "x.js"),
		filepath.Join(tmp, "vendor", "lib", "x.go"),
		filepath.Join(tmp, ".coddy", "worktrees", "feat", "x.go"),
		filepath.Join(tmp, "main.go"),                 // the root's folder is the catalog's
		filepath.Join(filepath.Dir(tmp), "x", "y.go"), // outside the workspace
		"",
	} {
		if got := f.Nested(tmp, []string{p}, nil); len(got) != 0 {
			t.Fatalf("path %q must read no nested rule, got %v", p, ruleFiles(tmp, got))
		}
	}
}

// A nested rule applies to its folder and below, never above or beside: its
// globs are anchored at its folder, and a rule a glob gates waits for a file
// both under its folder and matching the glob.
func TestMatchNestedRulesWithinTheirSubtree(t *testing.T) {
	tmp := nestedGroup(t)
	nested := rules.DefaultFactory("").Nested(tmp, []string{"db/x", "mobile/x"}, nil)
	if len(nested) != 2 {
		t.Fatalf("nested rules = %v", ruleFiles(tmp, nested))
	}
	at := func(p string) string { return filepath.Join(tmp, filepath.FromSlash(p)) }
	cases := []struct {
		file string
		want []string
	}{
		{"db/migrations/0001.sql", []string{"db/.coddy/rules/db.mdc"}},
		{"db/schema.sql", nil},
		{"migrations/0001.sql", nil},
		{"other/db/migrations/0001.sql", nil},
		{"mobile/src/app.ts", []string{"mobile/.cursor/rules/mobile.mdc"}},
		{"mobilex/app.ts", nil},
		{"app.ts", nil},
	}
	for _, tc := range cases {
		for name, match := range map[string]func([]*rules.Rule, []string) []*rules.Rule{
			"MatchScoped": rules.MatchScoped,
			"MatchAuto":   rules.MatchAuto,
		} {
			got := ruleFiles(tmp, match(nested, []string{at(tc.file)}))
			if len(got) != len(tc.want) || (len(got) == 1 && got[0] != tc.want[0]) {
				t.Fatalf("%s(%s) = %v, want %v", name, tc.file, got, tc.want)
			}
		}
	}
}

// A rule scoped to a folder and gated by globs anchored elsewhere still waits
// for a path inside its folder: the scope and the globs both have to hold.
func TestMatchScopedRuleNeedsTheScopeAndTheGlobs(t *testing.T) {
	root := t.TempDir()
	r := &rules.Rule{
		ID: "x", FilePath: filepath.Join(root, "db", ".cursor", "rules", "x.mdc"),
		AlwaysApply: true, ApplyMode: rules.ApplyAuto,
		Globs: []string{"**/*.sql"}, Root: root, ScopeDir: filepath.Join(root, "db"),
	}
	for file, want := range map[string]int{
		filepath.Join(root, "db", "a.sql"):  1,
		filepath.Join(root, "db", "a.go"):   0,
		filepath.Join(root, "web", "a.sql"): 0,
	} {
		if got := len(rules.MatchScoped([]*rules.Rule{r}, []string{file})); got != want {
			t.Fatalf("MatchScoped(%s) = %d, want %d", file, got, want)
		}
		if got := len(rules.MatchAuto([]*rules.Rule{r}, []string{file})); got != want {
			t.Fatalf("MatchAuto(%s) = %d, want %d", file, got, want)
		}
	}
}
