package rules_test

import (
	"os"
	"path/filepath"
	"sort"
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

func TestDiscoverNestedAgentsMD(t *testing.T) {
	tmp := t.TempDir()
	// Root AGENTS.md is the unconditional project docs preamble, not a rule.
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte("root preamble"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"internal/agent", "external/httpserver", ".git/sub", "node_modules/pkg"} {
		if err := os.MkdirAll(filepath.Join(tmp, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(tmp, "internal", "agent", "AGENTS.md"), []byte("agent loop notes"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "external", "httpserver", "AGENTS.md"), []byte("http notes"), 0o644)
	// Hidden and dependency dirs must be skipped.
	_ = os.WriteFile(filepath.Join(tmp, ".git", "sub", "AGENTS.md"), []byte("hidden"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "node_modules", "pkg", "AGENTS.md"), []byte("dep"), 0o644)

	got, err := rules.DefaultFactory().Discover(tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 nested AGENTS.md rules, got %d: %+v", len(got), got)
	}
	for _, r := range got {
		if r.Source != rules.SourceAgents {
			t.Fatalf("source = %q, want agents", r.Source)
		}
		if !r.AlwaysApply || r.ApplyMode != rules.ApplyAuto || len(r.Globs) != 0 {
			t.Fatalf("AGENTS.md rule must be an auto rule without globs: %+v", r)
		}
		if r.ScopeDir != filepath.Dir(r.FilePath) {
			t.Fatalf("AGENTS.md rule must be scoped to its own directory: ScopeDir=%q FilePath=%q", r.ScopeDir, r.FilePath)
		}
	}
	names := []string{got[0].CanonicalName(), got[1].CanonicalName()}
	sort.Strings(names)
	if names[0] != "external/httpserver/AGENTS.md" || names[1] != "internal/agent/AGENTS.md" {
		t.Fatalf("names = %v", names)
	}
}

func TestDiscoverNestedAgentsMDTruncatesOversizedFile(t *testing.T) {
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
	got, err := rules.DefaultFactory().Discover(tmp, rules.ParseSystems([]string{"agents"}))
	if err != nil {
		t.Fatal(err)
	}
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

	got, err := rules.DefaultFactory().Discover(tmp, rules.ParseSystems([]string{"agents"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("agents filter: expected 1 rule, got %d", len(got))
	}
	got, err = rules.DefaultFactory().Discover(tmp, rules.ParseSystems([]string{"coddy"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("coddy filter must exclude agents rules, got %d", len(got))
	}
}

func TestParseAtMentions(t *testing.T) {
	names := rules.ParseAtMentions("Use @foo in text")
	if len(names) != 1 || names[0] != "foo" {
		t.Fatalf("got %v", names)
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

// TestMatchGlob pins the matcher both dialects rely on: doublestar syntax
// (**, {a,b}) against the project-relative path, since context files arrive
// as absolute paths, plus the file-name leniency of a directory-less pattern.
func TestMatchGlob(t *testing.T) {
	root := filepath.Join("/proj")
	cases := []struct {
		name    string
		pattern string
		file    string
		want    bool
	}{
		{"any depth go file", "**/*.go", filepath.Join(root, "internal", "agent", "react.go"), true},
		{"root go file through **", "**/*.go", filepath.Join(root, "main.go"), true},
		{"directory prefix", "internal/**/*.go", filepath.Join(root, "internal", "agent", "react.go"), true},
		{"directory prefix, direct child", "internal/**/*.go", filepath.Join(root, "internal", "x.go"), true},
		{"directory prefix does not leak to siblings", "internal/**/*.go", filepath.Join(root, "external", "x.go"), false},
		{"deep prefix", "external/httpserver/**/*.go", filepath.Join(root, "external", "httpserver", "server.go"), true},
		{"brace group", "src/**/*.{ts,tsx}", filepath.Join(root, "src", "ui", "App.tsx"), true},
		{"brace group other alternative", "src/**/*.{ts,tsx}", filepath.Join(root, "src", "ui", "app.ts"), true},
		{"brace group rejects others", "src/**/*.{ts,tsx}", filepath.Join(root, "src", "ui", "app.css"), false},
		{"everything under a dir", "docs/**", filepath.Join(root, "docs", "a", "b.md"), true},
		{"single file", "README.md", filepath.Join(root, "README.md"), true},
		{"single file elsewhere", "README.md", filepath.Join(root, "docs", "README.md"), false},
		{"directory-less pattern is root only", "*.md", filepath.Join(root, "docs", "guide.md"), false},
		{"directory-less pattern matches in the root", "*.md", filepath.Join(root, "README.md"), true},
		{"any depth through **", "**/*.md", filepath.Join(root, "docs", "guide.md"), true},
		{"leading ./ is ignored", "./internal/**/*.go", filepath.Join(root, "internal", "x.go"), true},
		{"relative context path", "internal/**/*.go", filepath.Join("internal", "x.go"), true},
		{"file outside the project only matches unanchored patterns", "**/*.go", filepath.Join("/other", "x.go"), true},
		{"anchored pattern never matches outside the project", "internal/**/*.go", filepath.Join("/other", "internal", "x.go"), false},
		{"blank pattern", "   ", filepath.Join(root, "x.go"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rules.MatchGlob(tc.pattern, root, tc.file); got != tc.want {
				t.Fatalf("MatchGlob(%q, %q, %q) = %v, want %v", tc.pattern, root, tc.file, got, tc.want)
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
	catalog, err := rules.DefaultFactory().Discover(tmp, nil)
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

	got, err := rules.DefaultFactory().Discover(tmp, nil)
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

func TestDiscoverAgentsDirPrecedence(t *testing.T) {
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

	got, err := rules.DefaultFactory().Discover(tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected dup.mdc and dup.md once each, got %d", len(got))
	}
	for _, r := range got {
		if r.Source != rules.SourceAgentsDir {
			t.Fatalf("%s: source %q, want the shared folder to win over tool folders", filepath.Base(r.FilePath), r.Source)
		}
	}

	// Coddy's own folder still wins over the shared one.
	if err := os.MkdirAll(filepath.Join(tmp, ".coddy", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(tmp, ".coddy", "rules", "dup.mdc"), []byte("from coddy"), 0o644)
	got, err = rules.DefaultFactory().Discover(tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if filepath.Base(r.FilePath) == "dup.mdc" && r.Source != rules.SourceCoddy {
			t.Fatalf("dup.mdc: source %q, want coddy", r.Source)
		}
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

	only, err := rules.DefaultFactory().Discover(tmp, rules.ParseSystems([]string{"agents-dir"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].Source != rules.SourceAgentsDir {
		t.Fatalf("agents-dir filter: %+v", only)
	}
	// The AGENTS.md convention keeps its own id: "agents" does not pull in the folder.
	agentsOnly, err := rules.DefaultFactory().Discover(tmp, rules.ParseSystems([]string{"agents"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(agentsOnly) != 1 || agentsOnly[0].Source != rules.SourceAgents {
		t.Fatalf("agents filter: %+v", agentsOnly)
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
	_ = os.WriteFile(filepath.Join(tmp, "pkg", "AGENTS.md"), []byte("nested"), 0o644)

	var buf strings.Builder
	if err := rules.RenderCatalog(&buf, tmp, rules.DefaultFactory(), nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "FORMAT") {
		t.Fatalf("header lacks FORMAT column:\n%s", out)
	}
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
		if len(cells) >= 3 {
			rows[cells[2]] = cells
		}
	}
	if r := rows["go"]; len(r) < 3 || r[0] != "agents-dir" || r[1] != "cursor" {
		t.Fatalf("go row = %v", r)
	}
	if r := rows["api"]; len(r) < 3 || r[0] != "agents-dir" || r[1] != "claude" {
		t.Fatalf("api row = %v", r)
	}
	if r := rows["pkg/AGENTS.md"]; len(r) < 3 || r[0] != "agents" || r[1] != "agents.md" {
		t.Fatalf("AGENTS.md row = %v", r)
	}
	if !strings.Contains(out, "3 rule(s) under") {
		t.Fatalf("summary line missing:\n%s", out)
	}
}
