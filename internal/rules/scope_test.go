package rules_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/rules"
)

func scopedRule(id, dir, content string) *rules.Rule {
	return &rules.Rule{
		ID:          id,
		Name:        id,
		FilePath:    filepath.Join(dir, "AGENTS.md"),
		Source:      rules.SourceAgents,
		AlwaysApply: true,
		ApplyMode:   rules.ApplyAuto,
		Content:     content,
		ScopeDir:    dir,
	}
}

func TestMatchAutoScopedAgentsRule(t *testing.T) {
	scope := filepath.Join("/proj", "internal", "agent")
	r := scopedRule("agents:internal/agent", scope, "AGENT_SCOPE_TOKEN")
	catalog := []*rules.Rule{r}

	cases := []struct {
		name  string
		files []string
		want  int
	}{
		{"no context at all", nil, 0},
		{"file deep under the scope", []string{filepath.Join(scope, "sub", "react.go")}, 1},
		{"file directly in the scope", []string{filepath.Join(scope, "react.go")}, 1},
		{"the scope directory itself", []string{scope}, 1},
		{"sibling directory", []string{filepath.Join("/proj", "internal", "session", "state.go")}, 0},
		{"parent directory", []string{filepath.Join("/proj", "internal")}, 0},
		// The trailing separator in the prefix check is what makes this 0.
		{"prefix collision", []string{filepath.Join("/proj", "internal", "agentx", "f.go")}, 0},
		{"one of several paths matches", []string{
			filepath.Join("/proj", "docs", "x.md"),
			filepath.Join(scope, "react.go"),
		}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(rules.MatchAuto(catalog, tc.files)); got != tc.want {
				t.Fatalf("MatchAuto(%v) = %d matches, want %d", tc.files, got, tc.want)
			}
			if got := len(rules.MatchScoped(catalog, tc.files)); got != tc.want {
				t.Fatalf("MatchScoped(%v) = %d matches, want %d", tc.files, got, tc.want)
			}
		})
	}
}

// TestMatchScopedGlobRules: a tool call that targets a file matching a rule's
// globs activates it, the way Claude Code loads a paths rule when a matching
// file is read. Rules with neither a scope nor globs are ignored on purpose:
// they are on from the first turn and re-matching them per tool call is noise.
func TestMatchScopedGlobRules(t *testing.T) {
	root, err := filepath.Abs(filepath.FromSlash("/proj"))
	if err != nil {
		t.Fatal(err)
	}
	always := &rules.Rule{
		ID: "coddy:always", Name: "always",
		AlwaysApply: true, ApplyMode: rules.ApplyAuto, Content: "always body",
	}
	globbed := &rules.Rule{
		ID: "cursor:go", Name: "go",
		AlwaysApply: true, ApplyMode: rules.ApplyAuto,
		Globs:       []string{"internal/**/*.go"},
		Root:        root,
		Content:     "glob body",
	}
	mention := &rules.Rule{
		ID: "cursor:manual", Name: "manual",
		AlwaysApply: false, ApplyMode: rules.ApplyMention,
		Globs:       []string{"**/*.go"},
		Root:        root,
		Content:     "manual body",
	}
	catalog := []*rules.Rule{always, globbed, mention}

	got := rules.MatchScoped(catalog, []string{filepath.Join(root, "internal", "agent", "react.go")})
	if len(got) != 1 || got[0].ID != "cursor:go" {
		t.Fatalf("MatchScoped must return exactly the glob rule the path matches, got %+v", got)
	}
	if got := rules.MatchScoped(catalog, []string{filepath.Join(root, "docs", "x.md")}); len(got) != 0 {
		t.Fatalf("a path outside every glob must activate nothing, got %d", len(got))
	}
	if got := rules.MatchScoped(catalog, nil); len(got) != 0 {
		t.Fatalf("no paths must activate nothing, got %d", len(got))
	}
	// MatchAuto keeps its semantics: the always-on rule matches on any turn,
	// the glob rule on a matching context file, the mention rule never.
	if got := rules.MatchAuto(catalog, []string{filepath.Join(root, "internal", "x.go")}); len(got) != 2 {
		t.Fatalf("MatchAuto = %d, want 2", len(got))
	}
}

func TestMatchAutoScopedMentionRuleNeverMatches(t *testing.T) {
	r := scopedRule("agents:sub", filepath.Join("/proj", "sub"), "body")
	r.AlwaysApply = false
	r.ApplyMode = rules.ApplyMention
	catalog := []*rules.Rule{r}
	if got := rules.MatchAuto(catalog, []string{filepath.Join("/proj", "sub", "x.go")}); len(got) != 0 {
		t.Fatalf("mention rule must not auto-match, got %d", len(got))
	}
}

func TestScopedRuleStaysStickyAfterMatch(t *testing.T) {
	scope := filepath.Join("/proj", "internal", "agent")
	catalog := []*rules.Rule{scopedRule("agents:a", scope, "body")}

	sticky := rules.UnionStable(nil, rules.MatchAuto(catalog, []string{filepath.Join(scope, "react.go")}))
	if len(sticky) != 1 {
		t.Fatalf("sticky len %d after match, want 1", len(sticky))
	}
	// A later turn touching an unrelated directory must not drop it.
	sticky = rules.UnionStable(sticky, rules.MatchAuto(catalog, []string{filepath.Join("/proj", "docs", "x.md")}))
	if len(sticky) != 1 {
		t.Fatalf("sticky len %d after unrelated turn, want 1", len(sticky))
	}
}

func TestPathUnderDir(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		path string
		want bool
	}{
		{"identical", filepath.Join("/proj", "a"), filepath.Join("/proj", "a"), true},
		{"child", filepath.Join("/proj", "a"), filepath.Join("/proj", "a", "b.go"), true},
		{"grandchild", filepath.Join("/proj", "a"), filepath.Join("/proj", "a", "b", "c.go"), true},
		{"sibling", filepath.Join("/proj", "a"), filepath.Join("/proj", "b", "c.go"), false},
		{"prefix collision", filepath.Join("/proj", "a"), filepath.Join("/proj", "ax", "c.go"), false},
		{"parent", filepath.Join("/proj", "a"), "/proj", false},
		{"empty dir", "", filepath.Join("/proj", "a"), false},
		{"empty path", filepath.Join("/proj", "a"), "", false},
		{"blank path", filepath.Join("/proj", "a"), "   ", false},
		{"trailing separator on dir", filepath.Join("/proj", "a") + string(filepath.Separator), filepath.Join("/proj", "a", "b.go"), true},
		{"slash-form path against native dir", filepath.Join("/proj", "a"), "/proj/a/b.go", true},
		{"unclean path", filepath.Join("/proj", "a"), filepath.Join("/proj", "a", "..", "a", "b.go"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rules.PathUnderDir(tc.dir, tc.path); got != tc.want {
				t.Fatalf("PathUnderDir(%q, %q) = %v, want %v", tc.dir, tc.path, got, tc.want)
			}
		})
	}
}

func TestPathUnderDirWindowsCaseInsensitive(t *testing.T) {
	dir := filepath.Join("C:", "Proj", "Internal", "Agent")
	path := filepath.Join("c:", "proj", "internal", "agent", "react.go")
	got := rules.PathUnderDir(dir, path)
	want := runtime.GOOS == "windows"
	if got != want {
		t.Fatalf("PathUnderDir(%q, %q) = %v, want %v on %s", dir, path, got, want, runtime.GOOS)
	}
}

func TestPathsUnderDir(t *testing.T) {
	dir := filepath.Join("/proj", "a")
	if rules.PathsUnderDir(dir, nil) {
		t.Fatal("no paths must not match")
	}
	if !rules.PathsUnderDir(dir, []string{"/other", filepath.Join(dir, "x.go")}) {
		t.Fatal("one matching path is enough")
	}
	if rules.PathsUnderDir(dir, []string{"/other", "/elsewhere"}) {
		t.Fatal("unrelated paths must not match")
	}
}

func TestPromptOmitsScopedRuleUntilMatched(t *testing.T) {
	tmp := t.TempDir()
	scope := filepath.Join(tmp, "internal", "agent")
	catalog := []*rules.Rule{scopedRule("agents:a", scope, "SCOPED_BODY_TOKEN")}

	out, _ := rules.RenderPrompt("", tmp, rules.MatchAuto(catalog, nil), nil)
	if strings.Contains(out, "SCOPED_BODY_TOKEN") {
		t.Fatalf("untouched scoped rule leaked into the prompt: %q", out)
	}
	out, _ = rules.RenderPrompt("", tmp, rules.MatchAuto(catalog, []string{filepath.Join(scope, "react.go")}), nil)
	if !strings.Contains(out, "SCOPED_BODY_TOKEN") {
		t.Fatalf("touched scoped rule missing from the prompt: %q", out)
	}
}
