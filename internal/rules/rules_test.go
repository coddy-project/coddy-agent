package rules_test

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/rules"
)

func TestMatchAutoGlob(t *testing.T) {
	r := &rules.Rule{
		ID:          "coddy:/tmp/go.mdc",
		Name:        "go-standards",
		AlwaysApply: true,
		ApplyMode:   rules.ApplyAuto,
		Globs:       []string{"**/*.go"},
		Content:     "RULE_GLOB_TOKEN",
	}
	catalog := []*rules.Rule{r}
	if matched := rules.MatchAuto(catalog, []string{"/proj/main.go"}); len(matched) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matched))
	}
	if matched := rules.MatchAuto(catalog, nil); len(matched) != 0 {
		t.Fatalf("no context file must match nothing, got %d", len(matched))
	}
}

// TestAlwaysOnRules pins what a system prompt may carry: the rules that apply
// from the first turn whatever the session touches, and none that waits for a
// path or a mention.
func TestAlwaysOnRules(t *testing.T) {
	always := &rules.Rule{ID: "a", Name: "always", AlwaysApply: true, ApplyMode: rules.ApplyAuto}
	globbed := &rules.Rule{ID: "g", Name: "go", AlwaysApply: true, ApplyMode: rules.ApplyAuto, Globs: []string{"**/*.go"}}
	scoped := &rules.Rule{ID: "s", Name: "sub/AGENTS.md", AlwaysApply: true, ApplyMode: rules.ApplyAuto, ScopeDir: "/proj/sub"}
	manual := &rules.Rule{ID: "m", Name: "deploy", ApplyMode: rules.ApplyMention}

	got := rules.AlwaysOnRules([]*rules.Rule{always, globbed, nil, scoped, manual})
	if len(got) != 1 || got[0] != always {
		t.Fatalf("AlwaysOnRules = %+v, want the unconditional rule alone", got)
	}
	for _, r := range []*rules.Rule{globbed, scoped, manual, nil} {
		if r.AlwaysOn() {
			t.Fatalf("%+v must not count as always on", r)
		}
	}
	if !always.AlwaysOn() {
		t.Fatal("an auto rule without patterns or scope is always on")
	}

	// The loader sets AlwaysApply exactly when a rule is an auto rule, so the
	// files the documentation calls active immediately are always on.
	for path, src := range map[string]string{
		"plain.md":        "BODY",
		"described.md":    "---\ndescription: house style\n---\nBODY",
		"no-header.mdc":   "BODY",
		"always-true.mdc": "---\nalwaysApply: true\n---\nBODY",
	} {
		r, err := rules.ParseRuleFile(path, rules.SourceCoddy, []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if !r.AlwaysOn() {
			t.Fatalf("%s: %+v must be always on", path, r)
		}
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
}

func TestRenderPromptDedupe(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte("# Agents\n\nAlways read tests."), 0o644); err != nil {
		t.Fatal(err)
	}
	auto := &rules.Rule{ID: "a:1", Name: "a", AlwaysApply: true, ApplyMode: rules.ApplyAuto, Content: "auto body"}
	other := &rules.Rule{ID: "b:2", Name: "b", AlwaysApply: true, ApplyMode: rules.ApplyAuto, Content: "other body"}
	out, _ := rules.RenderPrompt("", tmp, []*rules.Rule{auto, nil, other, auto})
	if !strings.Contains(out, "AGENTS.md") {
		t.Fatal("missing agents")
	}
	if strings.Count(out, "auto body") != 1 {
		t.Fatal("dedupe failed for auto")
	}
	if !strings.Contains(out, "other body") {
		t.Fatal("missing the second rule")
	}
}

// writeRuleTree creates the named rule files under cwd, each holding its own
// path, so a test can tell which folder a rule came from.
func writeRuleTree(t testing.TB, cwd string, files ...string) {
	t.Helper()
	for _, f := range files {
		path := filepath.Join(cwd, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("---\nalwaysApply: true\n---\nfrom "+f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func ruleSources(rs []*rules.Rule) map[rules.Source]int {
	out := map[rules.Source]int{}
	for _, r := range rs {
		out[r.Source]++
	}
	return out
}

// TestDiscoverReadsOneProjectFolder is the duplicate of issue #366: a project
// that mirrors its Cursor rules for Claude Code hands the model one copy.
func TestDiscoverReadsOneProjectFolder(t *testing.T) {
	tmp := t.TempDir()
	writeRuleTree(t, tmp, ".cursor/rules/x.mdc", ".claude/rules/x.md", ".claude/rules/only-claude.md")

	got, err := rules.DefaultFactory("").Discover(tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the Cursor rule alone, got %d: %+v", len(got), got)
	}
	if got[0].Source != rules.SourceCursor || filepath.Base(got[0].FilePath) != "x.mdc" {
		t.Fatalf("rule = %s from %q, want x.mdc from cursor", got[0].FilePath, got[0].Source)
	}
}

// TestDiscoverProjectChainOrder walks the chain: whichever folders a project
// holds, the first of coddy, agents-dir, cursor, claude, codex is read alone.
func TestDiscoverProjectChainOrder(t *testing.T) {
	all := []string{".coddy/rules/c.mdc", ".agents/rules/a.mdc", ".cursor/rules/u.mdc", ".claude/rules/l.md", ".codex/rules/x.md"}
	want := []rules.Source{rules.SourceCoddy, rules.SourceAgentsDir, rules.SourceCursor, rules.SourceClaude, rules.SourceCodex}
	for i := range all {
		tmp := t.TempDir()
		writeRuleTree(t, tmp, all[i:]...)
		got, err := rules.DefaultFactory("").Discover(tmp, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Source != want[i] {
			t.Fatalf("folders %v: got %v, want one rule from %s", all[i:], ruleSources(got), want[i])
		}
	}
}

// TestDiscoverSkipsProjectFolderWithoutRuleFiles: a folder counts only when it
// holds a .md or .mdc file; an empty one, or one of Codex's *.rules policy
// files, leaves the chain to the next.
func TestDiscoverSkipsProjectFolderWithoutRuleFiles(t *testing.T) {
	tmp := t.TempDir()
	for _, dir := range []string{".coddy/rules", ".cursor/rules/nested"} {
		if err := os.MkdirAll(filepath.Join(tmp, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(tmp, ".cursor", "rules", "nested", "notes.txt"), []byte("not a rule"), 0o644)
	writeRuleTree(t, tmp, ".claude/rules/style.md")

	got, err := rules.DefaultFactory("").Discover(tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Source != rules.SourceClaude {
		t.Fatalf("got %v, want the Claude Code folder", ruleSources(got))
	}
}

// TestDiscoverKeepsAFolderWithAnUnreadableSubfolder: a subfolder that cannot be
// read costs its own rules only, so the chain does not pass over the folder to
// another agent's copy of the same rules.
func TestDiscoverKeepsAFolderWithAnUnreadableSubfolder(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits do not hide a folder here")
	}
	tmp := t.TempDir()
	writeRuleTree(t, tmp, ".coddy/rules/house.mdc", ".coddy/rules/locked/secret.mdc", ".cursor/rules/workflow.mdc")
	locked := filepath.Join(tmp, ".coddy", "rules", "locked")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	got, err := rules.DefaultFactory("").Discover(tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Source != rules.SourceCoddy || filepath.Base(got[0].FilePath) != "house.mdc" {
		t.Fatalf("got %v, want house.mdc from .coddy/rules alone", ruleSources(got))
	}
}

// TestDiscoverSystemsNarrowTheChain: rules.systems takes folders out of the
// chain, and the first admitted folder that holds rules is read.
func TestDiscoverSystemsNarrowTheChain(t *testing.T) {
	tmp := t.TempDir()
	writeRuleTree(t, tmp, ".coddy/rules/c.mdc", ".cursor/rules/u.mdc", ".claude/rules/l.md")

	got, err := rules.DefaultFactory("").Discover(tmp, rules.ParseSystems([]string{"cursor", "claude"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Source != rules.SourceCursor {
		t.Fatalf("cursor+claude filter: got %v, want cursor", ruleSources(got))
	}
	got, err = rules.DefaultFactory("").Discover(tmp, rules.ParseSystems([]string{"claude"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Source != rules.SourceClaude {
		t.Fatalf("claude filter: got %v, want claude", ruleSources(got))
	}
	got, err = rules.DefaultFactory("").Discover(tmp, rules.ParseSystems([]string{"agents-dir"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("a filter naming a folder the project lacks reads nothing, got %v", ruleSources(got))
	}
}

// TestInspectNamesTheFoldersItReadAndSkipped is what `coddy rules list`
// explains under its table: the folder the project rules came from, and the
// folders further down the chain that hold rules too.
func TestInspectNamesTheFoldersItReadAndSkipped(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeRuleTree(t, home, "rules/mine.md")
	writeRuleTree(t, cwd, ".cursor/rules/u.mdc", ".claude/rules/l.md")
	if err := os.MkdirAll(filepath.Join(cwd, ".codex", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(cwd, ".codex", "rules", "default.rules"), []byte("prefix_rule()"), 0o644)

	d, err := rules.DefaultFactory(home).Inspect(cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.ProjectFolder != ".cursor/rules" {
		t.Fatalf("ProjectFolder = %q, want .cursor/rules", d.ProjectFolder)
	}
	if len(d.Skipped) != 1 || d.Skipped[0] != ".claude/rules" {
		t.Fatalf("Skipped = %v, want .claude/rules alone (.codex/rules holds no rule file)", d.Skipped)
	}
	if d.UserFolder != rules.UserRulesDir(home) {
		t.Fatalf("UserFolder = %q, want %q", d.UserFolder, rules.UserRulesDir(home))
	}
	if got := ruleSources(d.Rules); got[rules.SourceCursor] != 1 || got[rules.SourceUser] != 1 || len(got) != 2 {
		t.Fatalf("rules = %v, want the cursor rule and the operator's", got)
	}
	if chain := rules.DefaultFactory("").ProjectFolders(); strings.Join(chain, " ") != ".coddy/rules .agents/rules .cursor/rules .claude/rules .codex/rules" {
		t.Fatalf("ProjectFolders = %v", chain)
	}
	if strings.Join(d.Chain, " ") != ".coddy/rules .agents/rules .cursor/rules .claude/rules .codex/rules" {
		t.Fatalf("Chain = %v, want every folder", d.Chain)
	}
	// rules.systems takes folders out of the chain the pass reports as well.
	narrowed, err := rules.DefaultFactory(home).Inspect(cwd, rules.ParseSystems([]string{"claude", "cursor"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(narrowed.Chain, " ") != ".cursor/rules .claude/rules" || narrowed.ProjectFolder != ".cursor/rules" {
		t.Fatalf("narrowed = %+v", narrowed)
	}
}

// TestInspectReportsAnUnreadableFolder: a folder of the chain that exists and
// cannot be read is passed over, and the listing says so instead of showing the
// next agent's rules as if nothing had happened.
func TestInspectReportsAnUnreadableFolder(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits do not hide a folder here")
	}
	cwd := t.TempDir()
	writeRuleTree(t, cwd, ".coddy/rules/house.mdc", ".cursor/rules/workflow.mdc")
	locked := filepath.Join(cwd, ".coddy", "rules")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	d, err := rules.DefaultFactory("").Inspect(cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.ProjectFolder != ".cursor/rules" || len(d.Unreadable) != 1 || !strings.HasPrefix(d.Unreadable[0], ".coddy/rules (") {
		t.Fatalf("inspect = %+v", d)
	}
	var buf strings.Builder
	if err := rules.RenderCatalog(&buf, cwd, rules.DefaultFactory(""), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Could not read: .coddy/rules (") {
		t.Fatalf("the listing does not name the unreadable folder:\n%s", buf.String())
	}
}

// agentsChainProject builds a root AGENTS.md and nested ones under a, a/b,
// a/other, node_modules/pkg and .git/sub, plus the file a/b/c/f.go.
func agentsChainProject(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte("root preamble"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"a", "a/b", "a/b/c", "a/other", "node_modules/pkg", ".git/sub"} {
		if err := os.MkdirAll(filepath.Join(tmp, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{"a", "a/b", "a/other", "node_modules/pkg", ".git/sub"} {
		if err := os.WriteFile(filepath.Join(tmp, filepath.FromSlash(dir), "AGENTS.md"), []byte("notes for "+dir), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "a", "b", "c", "f.go"), []byte("package c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return tmp
}

func agentsNames(rs []*rules.Rule) []string {
	var names []string
	for _, r := range rs {
		names = append(names, r.CanonicalName())
	}
	sort.Strings(names)
	return names
}

func TestNestedAgentsMDAreNeverWalked(t *testing.T) {
	tmp := agentsChainProject(t)
	got, err := rules.DefaultFactory("").Discover(tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.Source == rules.SourceAgents {
			t.Fatalf("session start must not read nested AGENTS.md files, found %q", r.FilePath)
		}
	}
}

func TestAgentsForPathsReadsTheChainOfTheTouchedFolder(t *testing.T) {
	tmp := agentsChainProject(t)

	got := rules.AgentsForPaths(tmp, []string{filepath.Join(tmp, "a", "b", "c", "f.go")}, nil)
	if names := agentsNames(got); len(names) != 2 || names[0] != "a/AGENTS.md" || names[1] != "a/b/AGENTS.md" {
		t.Fatalf("chain for a/b/c/f.go = %v, want a and a/b only", names)
	}
	for _, r := range got {
		if r.Source != rules.SourceAgents || r.Format != rules.FormatAgentsMD {
			t.Fatalf("source/format = %q/%q", r.Source, r.Format)
		}
		if !r.AlwaysApply || r.ApplyMode != rules.ApplyAuto || len(r.Globs) != 0 {
			t.Fatalf("AGENTS.md rule must be an auto rule without globs: %+v", r)
		}
		if r.ScopeDir != filepath.Dir(r.FilePath) || r.Root != tmp {
			t.Fatalf("scope/root: ScopeDir=%q FilePath=%q Root=%q", r.ScopeDir, r.FilePath, r.Root)
		}
		if !strings.HasPrefix(r.Content, "notes for ") {
			t.Fatalf("content = %q", r.Content)
		}
	}

	// A directory path includes its own AGENTS.md; a relative path resolves
	// against root.
	if names := agentsNames(rules.AgentsForPaths(tmp, []string{"a/other"}, nil)); len(names) != 2 || names[1] != "a/other/AGENTS.md" {
		t.Fatalf("chain for the a/other directory = %v", names)
	}
	// A file that does not exist yet (a write) reads its parent chain.
	if names := agentsNames(rules.AgentsForPaths(tmp, []string{filepath.Join(tmp, "a", "new.go")}, nil)); len(names) != 1 || names[0] != "a/AGENTS.md" {
		t.Fatalf("chain for a file about to be written = %v", names)
	}
}

func TestAgentsForPathsSkipsWhatDiscoveryNeverEnters(t *testing.T) {
	tmp := agentsChainProject(t)
	for _, p := range []string{
		filepath.Join(tmp, "node_modules", "pkg", "x.js"),
		filepath.Join(tmp, ".git", "sub", "x"),
		filepath.Join(tmp, "README.md"),          // root itself: the preamble, not a rule
		filepath.Join(filepath.Dir(tmp), "x.go"), // outside the project
		"",
	} {
		if got := rules.AgentsForPaths(tmp, []string{p}, nil); len(got) != 0 {
			t.Fatalf("path %q must load no nested AGENTS.md, got %v", p, agentsNames(got))
		}
	}
}

func TestAgentsForPathsDoesNotReadActiveRulesAgain(t *testing.T) {
	tmp := agentsChainProject(t)
	first := rules.AgentsForPaths(tmp, []string{filepath.Join(tmp, "a", "b", "c", "f.go")}, nil)
	again := rules.AgentsForPaths(tmp, []string{filepath.Join(tmp, "a", "b", "c", "f.go")}, first)
	if len(again) != 0 {
		t.Fatalf("rules already active must not come back, got %v", agentsNames(again))
	}
	deeper := rules.AgentsForPaths(tmp, []string{filepath.Join(tmp, "a", "other", "y.go")}, first)
	if names := agentsNames(deeper); len(names) != 1 || names[0] != "a/other/AGENTS.md" {
		t.Fatalf("only the folder not yet entered is read, got %v", names)
	}
}

func TestAgentsOnDemandFollowsRulesSystems(t *testing.T) {
	if !rules.AgentsOnDemand(nil) || !rules.AgentsOnDemand([]rules.Source{rules.SourceCoddy, rules.SourceAgents}) {
		t.Fatal("an empty list and a list naming agents both admit nested AGENTS.md")
	}
	if rules.AgentsOnDemand([]rules.Source{rules.SourceCoddy}) {
		t.Fatal("a list without agents must switch the on-demand reading off")
	}
}

func TestAgentsForPathsTruncatesOversizedFile(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A scoped rule enters the prompt in one shot, so an oversized file must be
	// capped the same way the project docs preamble is.
	big := strings.Repeat("x", 300*1024)
	if err := os.WriteFile(filepath.Join(tmp, "sub", "AGENTS.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	got := rules.AgentsForPaths(tmp, []string{filepath.Join(tmp, "sub", "x.go")}, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(got))
	}
	if len(got[0].Content) >= len(big) {
		t.Fatalf("content not capped: %d bytes", len(got[0].Content))
	}
	if !strings.HasSuffix(got[0].Content, "...(truncated)") {
		t.Fatal("capped content must be marked as truncated")
	}
}

func TestDiscoverAgentsMDSystemsFilter(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(tmp, "sub", "AGENTS.md"), []byte("sub notes"), 0o644)

	// No system makes session start read a nested AGENTS.md: "agents" only
	// admits the on-demand reading, "coddy" switches it off.
	for _, system := range []string{"agents", "coddy"} {
		got, err := rules.DefaultFactory("").Discover(tmp, rules.ParseSystems([]string{system}))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("%s filter: discovery must not read nested AGENTS.md, got %d", system, len(got))
		}
	}
	if !rules.AgentsOnDemand(rules.ParseSystems([]string{"agents"})) {
		t.Fatal("agents must admit the on-demand reading")
	}
	if rules.AgentsOnDemand(rules.ParseSystems([]string{"coddy"})) {
		t.Fatal("coddy alone must switch the on-demand reading off")
	}
}

// --- dialect by extension ----------------------------------------------------

// TestParseRuleFileCursorDialect covers the .mdc dialect: Cursor frontmatter
// with comma-separated globs (which is not valid YAML when a pattern starts
// with "*"), alwaysApply as the master switch, and Cursor's default of a
// description-only rule being reachable through @mention only.
func TestParseRuleFileCursorDialect(t *testing.T) {
	cases := []struct {
		name        string
		content     string
		wantMode    rules.ApplyMode
		wantAlways  bool
		wantGlobs   []string
		wantDesc    string
		wantContent string
	}{
		{
			name:        "comma separated unquoted globs with alwaysApply false auto-attach",
			content:     "---\ndescription: HTTP layer\nglobs: external/httpserver/**/*.go, docs/**/*.md\nalwaysApply: false\n---\n\nBODY",
			wantMode:    rules.ApplyAuto,
			wantAlways:  true,
			wantGlobs:   []string{"external/httpserver/**/*.go", "docs/**/*.md"},
			wantDesc:    "HTTP layer",
			wantContent: "BODY",
		},
		{
			name:       "star-leading glob keeps the rest of the frontmatter",
			content:    "---\ndescription: Go style\nglobs: **/*.go\nalwaysApply: true\n---\nBODY",
			wantMode:   rules.ApplyAuto,
			wantAlways: true,
			wantGlobs:  []string{"**/*.go"},
			wantDesc:   "Go style",
		},
		{
			name:       "yaml list globs",
			content:    "---\nglobs:\n  - \"src/**/*.ts\"\n  - 'lib/**/*.ts'\nalwaysApply: false\n---\nBODY",
			wantMode:   rules.ApplyAuto,
			wantAlways: true,
			wantGlobs:  []string{"src/**/*.ts", "lib/**/*.ts"},
		},
		{
			name:       "flow list globs",
			content:    "---\nglobs: ['**/*.go', '**/*.md']\nalwaysApply: true\n---\nBODY",
			wantMode:   rules.ApplyAuto,
			wantAlways: true,
			wantGlobs:  []string{"**/*.go", "**/*.md"},
		},
		{
			name:       "brace groups are not split on their commas",
			content:    "---\nglobs: src/**/*.{ts,tsx}, lib/**/*.ts\nalwaysApply: false\n---\nBODY",
			wantMode:   rules.ApplyAuto,
			wantAlways: true,
			wantGlobs:  []string{"src/**/*.{ts,tsx}", "lib/**/*.ts"},
		},
		{
			name:       "alwaysApply true without globs is on immediately",
			content:    "---\nalwaysApply: true\n---\nBODY",
			wantMode:   rules.ApplyAuto,
			wantAlways: true,
		},
		{
			name:       "alwaysApply false without globs is mention only",
			content:    "---\ndescription: Runbook\nalwaysApply: false\n---\nBODY",
			wantMode:   rules.ApplyMention,
			wantAlways: false,
			wantDesc:   "Runbook",
		},
		{
			name:       "description only follows Cursor's default of alwaysApply false",
			content:    "---\ndescription: Deploy runbook\n---\nBODY",
			wantMode:   rules.ApplyMention,
			wantAlways: false,
			wantDesc:   "Deploy runbook",
		},
		{
			name:       "empty frontmatter is a manual rule",
			content:    "---\n---\nBODY",
			wantMode:   rules.ApplyMention,
			wantAlways: false,
		},
		{
			// Unknown keys alone still make a header: Cursor's alwaysApply
			// defaults to false, whether the YAML parses or not.
			name:       "unknown keys only is a manual rule",
			content:    "---\ntitle: Notes\nauthor: Team: Core\n---\nBODY",
			wantMode:   rules.ApplyMention,
			wantAlways: false,
		},
		{
			name:        "no frontmatter is on immediately",
			content:     "BODY only",
			wantMode:    rules.ApplyAuto,
			wantAlways:  true,
			wantContent: "BODY only",
		},
		{
			name:       "crlf line endings",
			content:    "---\r\ndescription: Windows\r\nglobs: **/*.go\r\nalwaysApply: true\r\n---\r\nBODY\r\n",
			wantMode:   rules.ApplyAuto,
			wantAlways: true,
			wantGlobs:  []string{"**/*.go"},
			wantDesc:   "Windows",
		},
		{
			name:       "inline comment after a value",
			content:    "---\nglobs: **/*.go # every Go file\nalwaysApply: true # sticky\n---\nBODY",
			wantMode:   rules.ApplyAuto,
			wantAlways: true,
			wantGlobs:  []string{"**/*.go"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := rules.ParseRuleFile(filepath.Join("/proj", ".cursor", "rules", "x.mdc"), rules.SourceCursor, []byte(tc.content))
			if err != nil {
				t.Fatal(err)
			}
			if r.Format != rules.FormatCursor {
				t.Fatalf("format = %q, want cursor", r.Format)
			}
			if r.ApplyMode != tc.wantMode || r.AlwaysApply != tc.wantAlways {
				t.Fatalf("mode = %q always = %v, want %q / %v", r.ApplyMode, r.AlwaysApply, tc.wantMode, tc.wantAlways)
			}
			if strings.Join(r.Globs, "|") != strings.Join(tc.wantGlobs, "|") {
				t.Fatalf("globs = %q, want %q", r.Globs, tc.wantGlobs)
			}
			if r.Description != tc.wantDesc {
				t.Fatalf("description = %q, want %q", r.Description, tc.wantDesc)
			}
			if tc.wantContent != "" && r.Content != tc.wantContent {
				t.Fatalf("content = %q, want %q", r.Content, tc.wantContent)
			}
			if strings.Contains(r.Content, "---") || strings.Contains(r.Content, "alwaysApply") {
				t.Fatalf("frontmatter leaked into the body: %q", r.Content)
			}
		})
	}
}

// TestParseRuleFileClaudeDialect covers the .md dialect: Claude Code rules
// name their patterns "paths" and know no alwaysApply, so a rule without
// paths is loaded unconditionally and one with paths waits for a match.
func TestParseRuleFileClaudeDialect(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		wantMode   rules.ApplyMode
		wantAlways bool
		wantGlobs  []string
		wantDesc   string
	}{
		{
			name:       "paths list waits for a matching file",
			content:    "---\npaths:\n  - \"src/api/**/*.ts\"\n  - \"lib/**/*.{ts,tsx}\"\n---\n# API\nBODY",
			wantMode:   rules.ApplyAuto,
			wantAlways: true,
			wantGlobs:  []string{"src/api/**/*.ts", "lib/**/*.{ts,tsx}"},
		},
		{
			name:       "description only is loaded unconditionally",
			content:    "---\ndescription: House style\n---\nBODY",
			wantMode:   rules.ApplyAuto,
			wantAlways: true,
			wantDesc:   "House style",
		},
		{
			name:       "empty frontmatter is loaded unconditionally",
			content:    "---\n---\nBODY",
			wantMode:   rules.ApplyAuto,
			wantAlways: true,
		},
		{
			name:       "unknown keys only is loaded unconditionally",
			content:    "---\ntitle: Notes\nauthor: Team: Core\n---\nBODY",
			wantMode:   rules.ApplyAuto,
			wantAlways: true,
		},
		{
			name:       "no frontmatter is loaded unconditionally",
			content:    "BODY",
			wantMode:   rules.ApplyAuto,
			wantAlways: true,
		},
		{
			name:       "paths as a single string",
			content:    "---\npaths: \"internal/**/*.go\"\n---\nBODY",
			wantMode:   rules.ApplyAuto,
			wantAlways: true,
			wantGlobs:  []string{"internal/**/*.go"},
		},
		{
			name:       "globs is accepted as an alias of paths",
			content:    "---\nglobs: internal/**/*.go\n---\nBODY",
			wantMode:   rules.ApplyAuto,
			wantAlways: true,
			wantGlobs:  []string{"internal/**/*.go"},
		},
		{
			name:       "an explicit alwaysApply false is still honoured",
			content:    "---\ndescription: Manual notes\nalwaysApply: false\n---\nBODY",
			wantMode:   rules.ApplyMention,
			wantAlways: false,
			wantDesc:   "Manual notes",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := rules.ParseRuleFile(filepath.Join("/proj", ".claude", "rules", "x.md"), rules.SourceClaude, []byte(tc.content))
			if err != nil {
				t.Fatal(err)
			}
			if r.Format != rules.FormatClaude {
				t.Fatalf("format = %q, want claude", r.Format)
			}
			if r.ApplyMode != tc.wantMode || r.AlwaysApply != tc.wantAlways {
				t.Fatalf("mode = %q always = %v, want %q / %v", r.ApplyMode, r.AlwaysApply, tc.wantMode, tc.wantAlways)
			}
			if strings.Join(r.Globs, "|") != strings.Join(tc.wantGlobs, "|") {
				t.Fatalf("globs = %q, want %q", r.Globs, tc.wantGlobs)
			}
			if r.Description != tc.wantDesc {
				t.Fatalf("description = %q, want %q", r.Description, tc.wantDesc)
			}
			if !strings.Contains(r.Content, "BODY") || strings.Contains(r.Content, "---") {
				t.Fatalf("body = %q", r.Content)
			}
		})
	}
}

// TestParseRuleFileSameFrontmatterDiffersByExtension is the point of the
// dialects: one description-only frontmatter is a manual rule as .mdc and an
// unconditional one as .md.
func TestParseRuleFileSameFrontmatterDiffersByExtension(t *testing.T) {
	content := []byte("---\ndescription: Conventions\n---\nBODY")
	mdc, err := rules.ParseRuleFile(filepath.Join("/proj", ".agents", "rules", "conventions.mdc"), rules.SourceAgentsDir, content)
	if err != nil {
		t.Fatal(err)
	}
	md, err := rules.ParseRuleFile(filepath.Join("/proj", ".agents", "rules", "conventions.md"), rules.SourceAgentsDir, content)
	if err != nil {
		t.Fatal(err)
	}
	if mdc.Format != rules.FormatCursor || mdc.ApplyMode != rules.ApplyMention {
		t.Fatalf(".mdc: format %q mode %q, want cursor/mention", mdc.Format, mdc.ApplyMode)
	}
	if md.Format != rules.FormatClaude || md.ApplyMode != rules.ApplyAuto {
		t.Fatalf(".md: format %q mode %q, want claude/auto", md.Format, md.ApplyMode)
	}
}

func TestParseRuleFileRejectsUnknownExtension(t *testing.T) {
	if _, err := rules.ParseRuleFile(filepath.Join("/proj", ".agents", "rules", "policy.rules"), rules.SourceAgentsDir, []byte("body")); err == nil {
		t.Fatal("a file that is neither .md nor .mdc must not become a rule")
	}
}

// --- glob matching -------------------------------------------------------------

// absPath builds an absolute path from a slash-separated one on every OS:
// "/proj" stays "/proj" on POSIX and gains the current drive on Windows, so
// the anchoring logic under test sees a truly absolute root and file.
func absPath(t *testing.T, slashPath string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.FromSlash(slashPath))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// TestMatchGlob pins the matcher both dialects rely on: doublestar syntax
// (**, {a,b}) against the project-relative path, since context files arrive
// as absolute paths, with the root as a hard boundary.
func TestMatchGlob(t *testing.T) {
	root := absPath(t, "/proj")
	cases := []struct {
		name    string
		pattern string
		root    string
		file    string
		want    bool
	}{
		{"any depth go file", "**/*.go", root, filepath.Join(root, "internal", "agent", "react.go"), true},
		{"root go file through **", "**/*.go", root, filepath.Join(root, "main.go"), true},
		{"directory prefix", "internal/**/*.go", root, filepath.Join(root, "internal", "agent", "react.go"), true},
		{"directory prefix, direct child", "internal/**/*.go", root, filepath.Join(root, "internal", "x.go"), true},
		{"directory prefix does not leak to siblings", "internal/**/*.go", root, filepath.Join(root, "external", "x.go"), false},
		{"deep prefix", "external/httpserver/**/*.go", root, filepath.Join(root, "external", "httpserver", "server.go"), true},
		{"brace group", "src/**/*.{ts,tsx}", root, filepath.Join(root, "src", "ui", "App.tsx"), true},
		{"brace group other alternative", "src/**/*.{ts,tsx}", root, filepath.Join(root, "src", "ui", "app.ts"), true},
		{"brace group rejects others", "src/**/*.{ts,tsx}", root, filepath.Join(root, "src", "ui", "app.css"), false},
		{"everything under a dir", "docs/**", root, filepath.Join(root, "docs", "a", "b.md"), true},
		{"single file", "README.md", root, filepath.Join(root, "README.md"), true},
		{"single file elsewhere", "README.md", root, filepath.Join(root, "docs", "README.md"), false},
		{"directory-less pattern is root only", "*.md", root, filepath.Join(root, "docs", "guide.md"), false},
		{"directory-less pattern matches in the root", "*.md", root, filepath.Join(root, "README.md"), true},
		{"any depth through **", "**/*.md", root, filepath.Join(root, "docs", "guide.md"), true},
		{"leading ./ is ignored", "./internal/**/*.go", root, filepath.Join(root, "internal", "x.go"), true},
		{"relative context path is taken as root-relative", "internal/**/*.go", root, filepath.Join("internal", "x.go"), true},
		{"unclean path is cleaned first", "internal/**/*.go", root, filepath.Join(root, "docs", "..", "internal", "x.go"), true},
		{"unclean relative path is cleaned first", "docs/**", root, filepath.Join("internal", "..", "docs", "x.md"), true},
		// A relative path that climbs out of the root is outside it, the same
		// way an absolute one above the root is.
		{"relative escape is outside", "**/*.go", root, filepath.Join("..", "outside", "x.go"), false},
		{"relative escape through a child is outside", "**/*.go", root, filepath.Join("internal", "..", "..", "outside", "x.go"), false},
		{"the parent itself is outside", "**", root, "..", false},
		{"a file outside the project matches nothing", "**/*.go", root, absPath(t, "/other/x.go"), false},
		{"anchored pattern never matches outside the project", "internal/**/*.go", root, absPath(t, "/other/internal/x.go"), false},
		{"the parent of the root is outside", "**/*.go", root, absPath(t, "/x.go"), false},
		// The host path above the workspace must never leak into the match:
		// with root /tmp/proj, "tmp/**/*.go" is about a tmp/ directory inside
		// the project, not about where the checkout happens to live.
		{"host prefix never leaks into the match", "tmp/**/*.go", absPath(t, "/tmp/proj"), absPath(t, "/tmp/proj/internal/x.go"), false},
		{"a child directory starting with two dots is inside", "**/*.go", root, filepath.Join(root, "..cache", "x.go"), true},
		{"a child directory starting with two dots keeps its prefix", "..cache/**/*.go", root, filepath.Join(root, "..cache", "x.go"), true},
		{"without a root the file is matched as given", "**/*.go", "", absPath(t, "/other/x.go"), true},
		{"without a root an anchored pattern sees the whole path", "internal/**/*.go", "", filepath.Join(root, "internal", "x.go"), false},
		{"blank pattern", "   ", root, filepath.Join(root, "x.go"), false},
		{"blank file", "**/*.go", root, "  ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rules.MatchGlob(tc.pattern, tc.root, tc.file); got != tc.want {
				t.Fatalf("MatchGlob(%q, %q, %q) = %v, want %v", tc.pattern, tc.root, tc.file, got, tc.want)
			}
		})
	}
}

// TestMatchGlobWindowsPaths covers what only a Windows host can exercise:
// drive letters, case folding and mixed separators. CI runs this package on
// windows-latest; elsewhere the test is skipped.
func TestMatchGlobWindowsPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics")
	}
	cases := []struct {
		name    string
		pattern string
		root    string
		file    string
		want    bool
	}{
		{"drive letter root", "internal/**/*.go", `C:\proj`, `C:\proj\internal\agent\react.go`, true},
		{"case folds on the drive and the path", "internal/**/*.go", `C:\Proj`, `c:\proj\Internal\x.go`, true},
		{"mixed separators", "internal/**/*.go", `C:\proj`, `C:/proj/internal\x.go`, true},
		{"another volume is outside", "**/*.go", `C:\proj`, `D:\proj\internal\x.go`, false},
		{"sibling directory is outside", "**/*.go", `C:\proj`, `C:\other\x.go`, false},
		{"host prefix never leaks into the match", "proj/**/*.go", `C:\proj`, `C:\proj\proj\..\internal\x.go`, false},
		{"drive-relative path is outside", "**/*.go", `C:\proj`, `C:internal\x.go`, false},
		{"rooted path without a drive is outside", "**/*.go", `C:\proj`, `\proj\internal\x.go`, false},
		{"relative escape is outside", "**/*.go", `C:\proj`, `..\other\x.go`, false},
		{"relative child is inside", "internal/**/*.go", `C:\proj`, `internal\x.go`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rules.MatchGlob(tc.pattern, tc.root, tc.file); got != tc.want {
				t.Fatalf("MatchGlob(%q, %q, %q) = %v, want %v", tc.pattern, tc.root, tc.file, got, tc.want)
			}
		})
	}
}

func TestMatchAutoUsesProjectRelativeGlobs(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".agents", "rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "http.md"), []byte("---\npaths:\n  - \"internal/api/**/*.go\"\n---\nHTTP_BODY"), 0o644)
	catalog, err := rules.DefaultFactory("").Discover(tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 1 {
		t.Fatalf("catalog = %d rules, want 1", len(catalog))
	}
	if got := rules.MatchAuto(catalog, []string{filepath.Join(tmp, "internal", "api", "handler.go")}); len(got) != 1 {
		t.Fatalf("a file under internal/api must activate the rule, got %d", len(got))
	}
	if got := rules.MatchAuto(catalog, []string{filepath.Join(tmp, "internal", "agent", "react.go")}); len(got) != 0 {
		t.Fatalf("a file outside internal/api must not activate the rule, got %d", len(got))
	}
	if got := rules.MatchAuto(catalog, nil); len(got) != 0 {
		t.Fatalf("no context must not activate a path-scoped rule, got %d", len(got))
	}
}

// --- .agents/rules source ------------------------------------------------------

func TestDiscoverAgentsDirSource(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".agents", "rules", "backend")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "go.mdc"), []byte("---\nglobs: **/*.go\nalwaysApply: false\n---\nGO"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "style.md"), []byte("STYLE"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644)

	got, err := rules.DefaultFactory("").Discover(tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rules from .agents/rules, got %d: %+v", len(got), got)
	}
	abs, _ := filepath.Abs(tmp)
	for _, r := range got {
		if r.Source != rules.SourceAgentsDir {
			t.Fatalf("source = %q, want agents-dir", r.Source)
		}
		if r.Root != abs {
			t.Fatalf("root = %q, want %q", r.Root, abs)
		}
		switch filepath.Base(r.FilePath) {
		case "go.mdc":
			if r.Format != rules.FormatCursor || len(r.Globs) != 1 {
				t.Fatalf("go.mdc: %+v", r)
			}
		case "style.md":
			if r.Format != rules.FormatClaude || r.ApplyMode != rules.ApplyAuto {
				t.Fatalf("style.md: %+v", r)
			}
		default:
			t.Fatalf("unexpected rule %q", r.FilePath)
		}
	}
}

// TestDiscoverAgentsDirComesBeforeToolFolders: the shared folder is written
// for every agent, so it is read ahead of another agent's own folder, and both
// dialects in it are kept; Coddy's own folder still comes first.
func TestDiscoverAgentsDirComesBeforeToolFolders(t *testing.T) {
	tmp := t.TempDir()
	for _, sub := range []string{".cursor/rules", ".agents/rules", ".claude/rules"} {
		if err := os.MkdirAll(filepath.Join(tmp, filepath.FromSlash(sub)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(tmp, ".cursor", "rules", "dup.mdc"), []byte("from cursor"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, ".agents", "rules", "dup.mdc"), []byte("from agents dir"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, ".claude", "rules", "dup.md"), []byte("from claude"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, ".agents", "rules", "dup.md"), []byte("from agents dir md"), 0o644)

	got, err := rules.DefaultFactory("").Discover(tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected dup.mdc and dup.md of the shared folder, got %d", len(got))
	}
	for _, r := range got {
		if r.Source != rules.SourceAgentsDir {
			t.Fatalf("%s: source %q, want the shared folder alone", filepath.Base(r.FilePath), r.Source)
		}
	}

	if err := os.MkdirAll(filepath.Join(tmp, ".coddy", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(tmp, ".coddy", "rules", "dup.mdc"), []byte("from coddy"), 0o644)
	got, err = rules.DefaultFactory("").Discover(tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Source != rules.SourceCoddy {
		t.Fatalf("with .coddy/rules present: got %v, want coddy alone", ruleSources(got))
	}
}

func TestParseSystemsAgentsDir(t *testing.T) {
	got := rules.ParseSystems([]string{"agents-dir", " Agents-Dir ", "agents", "nonsense"})
	if len(got) != 3 || got[0] != rules.SourceAgentsDir || got[1] != rules.SourceAgentsDir || got[2] != rules.SourceAgents {
		t.Fatalf("ParseSystems = %v", got)
	}

	tmp := t.TempDir()
	for _, sub := range []string{".agents/rules", ".cursor/rules", "sub"} {
		if err := os.MkdirAll(filepath.Join(tmp, filepath.FromSlash(sub)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(tmp, ".agents", "rules", "shared.md"), []byte("shared"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, ".cursor", "rules", "cursor.mdc"), []byte("cursor"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "sub", "AGENTS.md"), []byte("nested"), 0o644)

	only, err := rules.DefaultFactory("").Discover(tmp, rules.ParseSystems([]string{"agents-dir"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].Source != rules.SourceAgentsDir {
		t.Fatalf("agents-dir filter: %+v", only)
	}
	// The AGENTS.md convention keeps its own id: "agents" does not pull in the
	// folder, and it has no provider to discover from. Nested files are read
	// on demand (AgentsForPaths) while the system is admitted.
	agentsOnly, err := rules.DefaultFactory("").Discover(tmp, rules.ParseSystems([]string{"agents"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(agentsOnly) != 0 {
		t.Fatalf("agents filter must discover nothing eagerly: %+v", agentsOnly)
	}
	if !rules.AgentsOnDemand(rules.ParseSystems([]string{"agents"})) || rules.AgentsOnDemand(rules.ParseSystems([]string{"agents-dir"})) {
		t.Fatal("agents admits the on-demand AGENTS.md reading, agents-dir does not")
	}
	if got := rules.AgentsForPaths(tmp, []string{filepath.Join(tmp, "sub", "x.go")}, nil); len(got) != 1 || got[0].Source != rules.SourceAgents {
		t.Fatalf("on-demand read of sub/AGENTS.md: %+v", got)
	}
}

// --- catalog rendering -----------------------------------------------------------

func TestRenderCatalogShowsFormat(t *testing.T) {
	tmp := t.TempDir()
	for _, sub := range []string{".agents/rules", "pkg"} {
		if err := os.MkdirAll(filepath.Join(tmp, filepath.FromSlash(sub)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(tmp, ".agents", "rules", "go.mdc"), []byte("---\ndescription: Go\nglobs: **/*.go\nalwaysApply: false\n---\nGO"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, ".agents", "rules", "api.md"), []byte("---\npaths: [\"internal/**/*.go\"]\n---\nAPI"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, ".agents", "rules", "style.md"), []byte("---\ndescription: House style\n---\nSTYLE"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "pkg", "AGENTS.md"), []byte("nested"), 0o644)

	var buf strings.Builder
	if err := rules.RenderCatalog(&buf, tmp, rules.DefaultFactory(""), nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "FORMAT") {
		t.Fatalf("header lacks FORMAT column:\n%s", out)
	}
	// Columns: SOURCE, FORMAT, NAME, APPLY, ALWAYS, ACTIVATES ON, DESCRIPTION.
	// Empty cells are dropped by the split below, so only the leading five
	// (never empty) are addressed by index.
	rows := map[string][]string{}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "│") {
			continue
		}
		var cells []string
		for _, c := range strings.Split(line, "│") {
			if s := strings.TrimSpace(c); s != "" {
				cells = append(cells, s)
			}
		}
		if len(cells) >= 5 {
			rows[cells[2]] = cells
		}
	}
	// ALWAYS answers "in every prompt?": gated rules say false even when
	// they are auto rules, an unconditional Claude rule says true.
	if r := rows["go"]; len(r) < 5 || r[0] != "agents-dir" || r[1] != "cursor" || r[3] != "auto" || r[4] != "false" {
		t.Fatalf("go row = %v", r)
	}
	if r := rows["api"]; len(r) < 5 || r[0] != "agents-dir" || r[1] != "claude" || r[3] != "auto" || r[4] != "false" {
		t.Fatalf("api row = %v", r)
	}
	if r := rows["style"]; len(r) < 5 || r[0] != "agents-dir" || r[1] != "claude" || r[3] != "auto" || r[4] != "true" {
		t.Fatalf("style row = %v", r)
	}
	// The nested AGENTS.md is not walked for the listing either; the note
	// says where it comes from instead.
	if _, listed := rows["pkg/AGENTS.md"]; listed {
		t.Fatalf("a nested document must not be listed:\n%s", out)
	}
	if !strings.Contains(out, "3 rule(s) under") {
		t.Fatalf("summary line missing:\n%s", out)
	}
	if !strings.Contains(out, "Nested AGENTS.md and DESIGN.md files are not listed: they are read on demand") {
		t.Fatalf("on-demand note missing:\n%s", out)
	}
}

// --- rules of the operator (${CODDY_HOME}/rules) ---------------------------------

// TestUserRulesFromAgentHome covers the sixth root: rule files that belong to
// the person running coddy rather than to a checkout, so they apply in every
// workspace without being copied into any of them.
func TestUserRulesFromAgentHome(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(home, "rules", "house.md"), []byte("---\ndescription: House style\n---\nHOUSE_BODY"), 0o644)
	_ = os.WriteFile(filepath.Join(home, "rules", "go.mdc"), []byte("---\nglobs: **/*.go\nalwaysApply: false\n---\nGO_BODY"), 0o644)

	got, err := rules.DefaultFactory(home).Discover(cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected both user rules, got %+v", got)
	}
	for _, r := range got {
		if r.Source != rules.SourceUser {
			t.Fatalf("%s: source %q, want user", filepath.Base(r.FilePath), r.Source)
		}
		// Globs of a user rule are anchored at the workspace, not at the
		// agent home the file came from, so **/*.go means the project's Go
		// files like any project rule.
		if r.Root != cwd {
			t.Fatalf("%s: root %q, want the workspace %q", filepath.Base(r.FilePath), r.Root, cwd)
		}
	}

	// Without a home there is no user root, and nothing else changes.
	none, err := rules.DefaultFactory("").Discover(cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("no agent home must discover nothing: %+v", none)
	}
}

// TestUserRulesLoseToProjectRules keeps the more specific file in charge: a
// project that ships style.md overrides the operator's own style.md.
func TestUserRulesLoseToProjectRules(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".coddy", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(home, "rules", "style.md"), []byte("FROM_USER"), 0o644)
	_ = os.WriteFile(filepath.Join(cwd, ".coddy", "rules", "style.md"), []byte("FROM_PROJECT"), 0o644)

	got, err := rules.DefaultFactory(home).Discover(cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected style.md once, got %+v", got)
	}
	if got[0].Source != rules.SourceCoddy || !strings.Contains(got[0].Content, "FROM_PROJECT") {
		t.Fatalf("style.md resolved to %q from %q, want the project copy", got[0].Content, got[0].Source)
	}
}

func TestParseSystemsUser(t *testing.T) {
	got := rules.ParseSystems([]string{"user", " User "})
	if len(got) != 2 || got[0] != rules.SourceUser || got[1] != rules.SourceUser {
		t.Fatalf("ParseSystems = %v", got)
	}

	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".coddy", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(home, "rules", "mine.md"), []byte("mine"), 0o644)
	_ = os.WriteFile(filepath.Join(cwd, ".coddy", "rules", "theirs.md"), []byte("theirs"), 0o644)

	only, err := rules.DefaultFactory(home).Discover(cwd, rules.ParseSystems([]string{"user"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].Source != rules.SourceUser {
		t.Fatalf("user filter: %+v", only)
	}
	without, err := rules.DefaultFactory(home).Discover(cwd, rules.ParseSystems([]string{"coddy"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(without) != 1 || without[0].Source != rules.SourceCoddy {
		t.Fatalf("coddy filter must leave the user root out: %+v", without)
	}
}

func TestUserRulesDir(t *testing.T) {
	if got := rules.UserRulesDir(""); got != "" {
		t.Fatalf("UserRulesDir(\"\") = %q, want empty", got)
	}
	home := filepath.FromSlash("/agent/home")
	if got, want := rules.UserRulesDir(home), filepath.Join(home, "rules"); got != want {
		t.Fatalf("UserRulesDir = %q, want %q", got, want)
	}
}

// TestRenderPromptReportsProjectDocs lets the caller skip a file the rules
// block already embedded, instead of sending the same AGENTS.md twice.
func TestRenderPromptReportsProjectDocs(t *testing.T) {
	tmp := t.TempDir()
	agents := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(agents, []byte("PROJECT_DOC"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, docs := rules.RenderPrompt("", tmp, nil)
	if !strings.Contains(out, "PROJECT_DOC") {
		t.Fatalf("prompt = %q, want the project doc", out)
	}
	if len(docs) != 1 || docs[0] != agents {
		t.Fatalf("embedded docs = %v, want [%s]", docs, agents)
	}

	empty := t.TempDir()
	if _, docs := rules.RenderPrompt("", empty, nil); len(docs) != 0 {
		t.Fatalf("a project without docs reported %v", docs)
	}
}

// TestRenderCatalogNamesTheUserRoot pins the footer line: when a rule of the
// operator is in the listing, the table is no longer "under <workspace>" alone,
// and a reader who cannot find the file in the checkout needs to be told where
// it is.
func TestRenderCatalogNamesTheUserRoot(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(home, "rules", "house.md"), []byte("HOUSE"), 0o644)

	var buf strings.Builder
	if err := rules.RenderCatalog(&buf, cwd, rules.DefaultFactory(home), nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, cwd) || !strings.Contains(out, rules.UserRulesDir(home)) {
		t.Fatalf("footer names neither the workspace nor the user root:\n%s", out)
	}

	// A workspace whose rules are all its own says nothing about a folder the
	// reader does not have.
	if err := os.MkdirAll(filepath.Join(cwd, ".coddy", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(cwd, ".coddy", "rules", "house.md"), []byte("PROJECT"), 0o644)
	buf.Reset()
	if err := rules.RenderCatalog(&buf, cwd, rules.DefaultFactory(home), nil); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); strings.Contains(out, rules.UserRulesDir(home)) {
		t.Fatalf("the project copy won, so the user root must not be named:\n%s", out)
	}
}

// TestRenderCatalogNamesTheFolderItRead: the lines under the table say which
// project folder the rules came from and which folders holding rules were left
// alone, so a mirror nobody sees in the listing is not a mystery either.
func TestRenderCatalogNamesTheFolderItRead(t *testing.T) {
	cwd := t.TempDir()
	writeRuleTree(t, cwd, ".cursor/rules/workflow.mdc", ".claude/rules/workflow.md")

	var buf strings.Builder
	if err := rules.RenderCatalog(&buf, cwd, rules.DefaultFactory(""), nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "1 rule(s) under "+cwd) {
		t.Fatalf("summary line missing:\n%s", out)
	}
	if !strings.Contains(out, "Project rules folder: .cursor/rules\n") {
		t.Fatalf("the folder read is not named:\n%s", out)
	}
	if !strings.Contains(out, "Not read: .claude/rules (") {
		t.Fatalf("the skipped folder is not named:\n%s", out)
	}

	// A project with one folder has nothing to explain about the others.
	lone := t.TempDir()
	writeRuleTree(t, lone, ".claude/rules/style.md")
	buf.Reset()
	if err := rules.RenderCatalog(&buf, lone, rules.DefaultFactory(""), nil); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); strings.Contains(out, "Not read:") || !strings.Contains(out, "Project rules folder: .claude/rules") {
		t.Fatalf("a single folder was explained wrongly:\n%s", out)
	}

	// A rule the skipped folder holds under a name the folder read lacks is
	// no mirror: it is named, since the session goes without it.
	writeRuleTree(t, cwd, ".claude/rules/claude-only.md")
	buf.Reset()
	if err := rules.RenderCatalog(&buf, cwd, rules.DefaultFactory(""), nil); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "Only in a folder not read: .claude/rules/claude-only.md\n") {
		t.Fatalf("the rule only the skipped folder holds is not named:\n%s", out)
	}

	// The explanation names the chain rules.systems left, not every folder.
	buf.Reset()
	if err := rules.RenderCatalog(&buf, cwd, rules.DefaultFactory(""), rules.ParseSystems([]string{"cursor", "claude"})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "the first of .cursor/rules, .claude/rules that holds a rule file") {
		t.Fatalf("the explanation names folders outside the chain:\n%s", buf.String())
	}
}

// TestAgentsForPathsReadsDesignBesideAgents covers the second document a folder
// can describe itself with: entering it reads AGENTS.md and DESIGN.md alike,
// whichever of the two is there.
func TestAgentsForPathsReadsDesignBesideAgents(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(tmp, "a", "AGENTS.md"), []byte("A_AGENTS"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "a", "DESIGN.md"), []byte("A_DESIGN"), 0o644)
	// Only a DESIGN.md: a folder is free to write one and not the other.
	_ = os.WriteFile(filepath.Join(tmp, "a", "b", "DESIGN.md"), []byte("B_DESIGN"), 0o644)

	got := rules.AgentsForPaths(tmp, []string{filepath.Join(tmp, "a", "b", "f.go")}, nil)
	names := agentsNames(got)
	want := []string{"a/AGENTS.md", "a/DESIGN.md", "a/b/DESIGN.md"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
	for _, r := range got {
		if r.ScopeDir == "" || r.Source != rules.SourceAgents {
			t.Fatalf("%s: scope %q source %q", r.Name, r.ScopeDir, r.Source)
		}
	}

	// A second call with the first result as active reads nothing again.
	if again := rules.AgentsForPaths(tmp, []string{filepath.Join(tmp, "a", "b", "f.go")}, got); len(again) != 0 {
		t.Fatalf("already active documents were read again: %v", agentsNames(again))
	}
}

// TestLoadProjectDocsPairsBothDirectories pins the preamble order: the agent
// home's pair first, then the workspace's, and whichever file is absent is
// simply not there.
func TestLoadProjectDocsPairsBothDirectories(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	_ = os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("HOME_AGENTS"), 0o644)
	_ = os.WriteFile(filepath.Join(home, "DESIGN.md"), []byte("HOME_DESIGN"), 0o644)
	_ = os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("PROJECT_AGENTS"), 0o644)
	_ = os.WriteFile(filepath.Join(cwd, "DESIGN.md"), []byte("PROJECT_DESIGN"), 0o644)

	docs := rules.LoadProjectDocs(home, cwd)
	var bodies []string
	for _, d := range docs {
		bodies = append(bodies, d.Content)
	}
	want := []string{"HOME_AGENTS", "HOME_DESIGN", "PROJECT_AGENTS", "PROJECT_DESIGN"}
	if len(bodies) != len(want) {
		t.Fatalf("docs = %v, want %v", bodies, want)
	}
	for i := range want {
		if bodies[i] != want[i] {
			t.Fatalf("docs = %v, want %v", bodies, want)
		}
	}

	// A home with only a DESIGN.md contributes that one and nothing else.
	onlyDesign := t.TempDir()
	_ = os.WriteFile(filepath.Join(onlyDesign, "DESIGN.md"), []byte("SOLO_DESIGN"), 0o644)
	docs = rules.LoadProjectDocs(onlyDesign, t.TempDir())
	if len(docs) != 1 || docs[0].Content != "SOLO_DESIGN" {
		t.Fatalf("a home with only a DESIGN.md gave %+v", docs)
	}

	// Without a home only the workspace speaks.
	if docs := rules.LoadProjectDocs("", cwd); len(docs) != 2 || docs[0].Content != "PROJECT_AGENTS" {
		t.Fatalf("no agent home gave %+v", docs)
	}
}
