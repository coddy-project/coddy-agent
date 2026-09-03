package subagents

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writeDef(t *testing.T, dir, rel, body string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const reviewerDef = `---
name: reviewer
description: Reviews a diff for correctness and reports findings.
model: fake/model
tools: read, grep, glob
permission_mode: accept_edits
max_turns: 8
---
You review code. Report findings by severity.
`

// ---- parsing ----

func TestParseReadsFrontmatterAndBody(t *testing.T) {
	def, err := Parse("/w/.coddy/agents/reviewer.md", []byte(reviewerDef))
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "reviewer" || !strings.HasPrefix(def.Description, "Reviews a diff") {
		t.Fatalf("name/description = %q/%q", def.Name, def.Description)
	}
	if def.Model != "fake/model" || def.PermissionMode != "accept_edits" || def.MaxTurns != 8 {
		t.Fatalf("scalar fields lost: %+v", def)
	}
	if strings.Join(def.Tools, ",") != "read,grep,glob" {
		t.Fatalf("comma-separated tools = %v", def.Tools)
	}
	if !strings.Contains(def.Role, "You review code") {
		t.Fatalf("role body lost: %q", def.Role)
	}
	if def.Digest == "" || len(def.Digest) != 64 {
		t.Fatalf("digest must be a sha256 hex string, got %q", def.Digest)
	}
}

func TestParseAcceptsClaudeCodeAliases(t *testing.T) {
	src := `---
description: alias check
disallowedTools:
  - write
  - edit
permissionMode: bypassPermissions
maxTurns: 3
tools:
  - read
  - context7__*
---
body
`
	def, err := Parse("/w/.coddy/agents/aliases.md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "aliases" {
		t.Fatalf("name must default to the file stem, got %q", def.Name)
	}
	if strings.Join(def.DisallowedTools, ",") != "write,edit" {
		t.Fatalf("disallowedTools alias = %v", def.DisallowedTools)
	}
	if def.PermissionMode != "bypass" {
		t.Fatalf("bypassPermissions must map to bypass, got %q", def.PermissionMode)
	}
	if def.MaxTurns != 3 {
		t.Fatalf("maxTurns alias = %d", def.MaxTurns)
	}
	if len(def.Tools) != 2 || def.Tools[1] != "context7__*" {
		t.Fatalf("list tools = %v", def.Tools)
	}
}

func TestParseNamesADirectoryDefinitionAfterTheDirectory(t *testing.T) {
	def, err := Parse("/w/.coddy/agents/docs-writer/AGENT.md", []byte("---\ndescription: d\n---\nrole\n"))
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "docs-writer" {
		t.Fatalf("name = %q, want the directory name", def.Name)
	}
}

func TestParseRejectsInvalidDefinitions(t *testing.T) {
	cases := map[string]string{
		"no frontmatter":      "just a body\n",
		"missing description": "---\nname: x\n---\nbody\n",
		"bad name":            "---\nname: Bad Name!\ndescription: d\n---\nbody\n",
		"bad mode":            "---\ndescription: d\nmode: yolo\n---\nbody\n",
		"bad permission":      "---\ndescription: d\npermission_mode: root\n---\nbody\n",
	}
	for label, src := range cases {
		if _, err := Parse("/w/.coddy/agents/x.md", []byte(src)); err == nil {
			t.Errorf("%s: expected an error", label)
		}
	}
}

func TestParseBoundsTheRoleBody(t *testing.T) {
	long := strings.Repeat("x", MaxRoleBytes+100)
	def, err := Parse("/w/.coddy/agents/long.md", []byte("---\ndescription: d\n---\n"+long))
	if err != nil {
		t.Fatal(err)
	}
	if len(def.Role) > MaxRoleBytes+len(roleTruncatedMarker) || !strings.HasSuffix(def.Role, roleTruncatedMarker) {
		t.Fatalf("role must be truncated with a marker, got %d bytes", len(def.Role))
	}
}

func TestParseCutsTheCatalogDescription(t *testing.T) {
	long := strings.Repeat("d", MaxDescriptionRunes+50)
	def, err := Parse("/w/.coddy/agents/long.md", []byte("---\ndescription: "+long+"\n---\nbody"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(def.Description)); got > MaxDescriptionRunes {
		t.Fatalf("description runes = %d, want <= %d", got, MaxDescriptionRunes)
	}
}

// ---- loader ----

func TestLoaderOrdersScopesAndLetsLaterDirsWin(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeDef(t, home, "agents/reviewer.md", "---\ndescription: user copy\n---\nuser role\n")
	writeDef(t, home, "agents/notes-taker.md", "---\ndescription: takes notes\n---\nnotes\n")
	writeDef(t, cwd, ".coddy/agents/reviewer.md", "---\ndescription: project copy\n---\nproject role\n")

	l := NewLoader([]string{"${CODDY_HOME}/agents", "${CWD}/.claude/agents", "${CWD}/.coddy/agents"}, "ask")
	defs := l.Load(cwd, home)

	byName := map[string]*Definition{}
	for _, d := range defs {
		byName[d.Name] = d
	}
	if got := byName["reviewer"]; got == nil || got.Description != "project copy" || got.Scope != ScopeProject {
		t.Fatalf("project definition must win: %+v", got)
	}
	if got := byName["notes-taker"]; got == nil || got.Scope != ScopeUser {
		t.Fatalf("user definition missing or mis-scoped: %+v", got)
	}
	for _, name := range []string{"general", "explore"} {
		if got := byName[name]; got == nil || !got.Builtin || got.Scope != ScopeBuiltin {
			t.Fatalf("built-in %s missing or mis-scoped: %+v", name, got)
		}
	}
}

func TestLoaderUserFileReplacesABuiltin(t *testing.T) {
	home := t.TempDir()
	writeDef(t, home, "agents/explore.md", "---\ndescription: my explorer\n---\nmine\n")
	defs := NewLoader([]string{"${CODDY_HOME}/agents"}, "ask").Load(t.TempDir(), home)
	got := FindByName(defs, "explore")
	if got == nil || got.Builtin || got.Description != "my explorer" {
		t.Fatalf("user explore must replace the built-in: %+v", got)
	}
}

func TestLoaderDenyPolicySkipsProjectDirectories(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	// A directory that cannot even be listed proves the loader never touched it.
	writeDef(t, cwd, ".coddy/agents/reviewer.md", reviewerDef)
	writeDef(t, home, "agents/mine.md", "---\ndescription: mine\n---\nmine\n")

	l := NewLoader([]string{"${CODDY_HOME}/agents", "${CWD}/.coddy/agents"}, "deny")
	var visited []string
	l.visit = func(dir string) { visited = append(visited, dir) }
	defs := l.Load(cwd, home)
	if FindByName(defs, "reviewer") != nil {
		t.Fatal("deny must not load project definitions")
	}
	if FindByName(defs, "mine") == nil {
		t.Fatal("deny must keep user definitions")
	}
	for _, v := range visited {
		if strings.HasPrefix(v, CanonicalWorkspace(cwd)) {
			t.Fatalf("deny must not read a project directory, visited %q", v)
		}
	}
}

func TestLoaderDecidesScopeOnCanonicalPaths(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	writeDef(t, real, ".coddy/agents/reviewer.md", reviewerDef)
	// The session cwd is the symlink; the directory is listed through the real path.
	defs := NewLoader([]string{filepath.Join(real, ".coddy/agents")}, "ask").Load(link, t.TempDir())
	got := FindByName(defs, "reviewer")
	if got == nil || got.Scope != ScopeProject {
		t.Fatalf("a directory under the symlinked cwd is still project scope: %+v", got)
	}
}

func TestLoaderFirstNameWinsInsideOneDirectory(t *testing.T) {
	home := t.TempDir()
	writeDef(t, home, "agents/a-first.md", "---\nname: twin\ndescription: first\n---\n1\n")
	writeDef(t, home, "agents/b-second.md", "---\nname: twin\ndescription: second\n---\n2\n")
	defs := NewLoader([]string{"${CODDY_HOME}/agents"}, "ask").Load(t.TempDir(), home)
	if got := FindByName(defs, "twin"); got == nil || got.Description != "first" {
		t.Fatalf("lexically first file must win, got %+v", got)
	}
}

func TestLoaderSkipsInvalidAndOversizedFiles(t *testing.T) {
	home := t.TempDir()
	writeDef(t, home, "agents/broken.md", "no frontmatter here\n")
	writeDef(t, home, "agents/huge.md", "---\ndescription: huge\n---\n"+strings.Repeat("z", MaxFileBytes+1))
	writeDef(t, home, "agents/fine.md", "---\ndescription: fine\n---\nok\n")
	defs := NewLoader([]string{"${CODDY_HOME}/agents"}, "ask").Load(t.TempDir(), home)
	if FindByName(defs, "broken") != nil || FindByName(defs, "huge") != nil {
		t.Fatal("invalid or oversized files must be skipped")
	}
	if FindByName(defs, "fine") == nil {
		t.Fatal("a valid neighbour must still load")
	}
}

func TestLoaderCapsDefinitionsPerDirectory(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < MaxDefinitionsPerDir+5; i++ {
		writeDef(t, home, "agents/"+strings.Repeat("a", 3)+string(rune('a'+i%26))+strings.Repeat("x", i/26+1)+".md", "---\ndescription: d\n---\nb\n")
	}
	defs := NewLoader([]string{"${CODDY_HOME}/agents"}, "ask").Load(t.TempDir(), home)
	loaded := 0
	for _, d := range defs {
		if !d.Builtin {
			loaded++
		}
	}
	if loaded != MaxDefinitionsPerDir {
		t.Fatalf("loaded %d definitions, want the cap %d", loaded, MaxDefinitionsPerDir)
	}
}

func TestBuiltinsHaveTheDocumentedShape(t *testing.T) {
	var explore, general *Definition
	for _, d := range Bundled() {
		switch d.Name {
		case "explore":
			explore = d
		case "general":
			general = d
		}
	}
	if explore == nil || general == nil {
		t.Fatal("bundled explore and general are required")
	}
	if len(general.Tools) != 0 {
		t.Fatalf("general inherits the parent's tools, got allowlist %v", general.Tools)
	}
	want := []string{"read", "keep_result", "glob", "grep", "print_tree", "websearch", "webfetch", "load_skill", "background_list", "background_output", "background_wait"}
	if strings.Join(explore.Tools, ",") != strings.Join(want, ",") {
		t.Fatalf("explore tools = %v, want %v", explore.Tools, want)
	}
	if explore.Allows("run_command") || explore.Allows("context7__search") {
		t.Fatal("explore must not allow run_command or MCP tools")
	}
}

// ---- trust ----

func TestTrustStoreRoundTripAndDigestBinding(t *testing.T) {
	home := t.TempDir()
	store := NewTrustStore(home)
	def, err := Parse("/w/.coddy/agents/reviewer.md", []byte(reviewerDef))
	if err != nil {
		t.Fatal(err)
	}
	ws := CanonicalWorkspace("/w")
	if store.Approved(ws, def.Name, def.Digest) {
		t.Fatal("nothing is approved before Approve")
	}
	if err := store.Approve(ws, def); err != nil {
		t.Fatal(err)
	}
	if !store.Approved(ws, def.Name, def.Digest) {
		t.Fatal("approval must be recorded")
	}
	if store.Approved(ws, def.Name, "other-digest") {
		t.Fatal("an edited file (different digest) is not approved")
	}
	if store.Approved(CanonicalWorkspace("/elsewhere"), def.Name, def.Digest) {
		t.Fatal("approval is bound to the workspace")
	}
	if _, err := os.Stat(filepath.Join(home, TrustFileName)); err != nil {
		t.Fatalf("receipt file missing: %v", err)
	}
	// A fresh store over the same home sees the receipt.
	if !NewTrustStore(home).Approved(ws, def.Name, def.Digest) {
		t.Fatal("receipts must persist across store instances")
	}
	removed, err := store.Revoke(ws, def.Name)
	if err != nil || !removed {
		t.Fatalf("revoke = %v, %v", removed, err)
	}
	if store.Approved(ws, def.Name, def.Digest) {
		t.Fatal("revoked approval must not match")
	}
}

func TestDecideTrustByScopeAndPolicy(t *testing.T) {
	home := t.TempDir()
	store := NewTrustStore(home)
	ws := CanonicalWorkspace(t.TempDir())
	project := &Definition{Name: "p", Scope: ScopeProject, Digest: "d1"}
	user := &Definition{Name: "u", Scope: ScopeUser, Digest: "d2"}
	builtin := &Definition{Name: "b", Scope: ScopeBuiltin, Builtin: true}

	if got := Decide(project, "ask", ws, store); got != TrustNeedsApproval {
		t.Fatalf("unapproved project under ask = %q", got)
	}
	if got := Decide(project, "allow", ws, store); got != TrustTrusted {
		t.Fatalf("project under allow = %q", got)
	}
	if got := Decide(user, "ask", ws, store); got != TrustTrusted {
		t.Fatalf("user scope is always trusted, got %q", got)
	}
	if got := Decide(builtin, "deny", ws, store); got != TrustTrusted {
		t.Fatalf("built-ins are always trusted, got %q", got)
	}
	if err := store.Approve(ws, project); err != nil {
		t.Fatal(err)
	}
	if got := Decide(project, "ask", ws, store); got != TrustTrusted {
		t.Fatalf("approved project under ask = %q", got)
	}
}

// ---- limiter ----

func TestLimiterRefusesPastTheLimitAndReleases(t *testing.T) {
	l := NewLimiter(2)
	r1, ok := l.TryAcquire()
	if !ok {
		t.Fatal("first acquire must succeed")
	}
	r2, ok := l.TryAcquire()
	if !ok {
		t.Fatal("second acquire must succeed")
	}
	if _, ok := l.TryAcquire(); ok {
		t.Fatal("third acquire must be refused")
	}
	if l.InFlight() != 2 {
		t.Fatalf("in flight = %d", l.InFlight())
	}
	r1()
	r1() // releasing twice must not free a second slot
	if l.InFlight() != 1 {
		t.Fatalf("in flight after one release = %d", l.InFlight())
	}
	if _, ok := l.TryAcquire(); !ok {
		t.Fatal("a freed slot must be reusable")
	}
	r2()
}

func TestLimiterLoweringBelowUsageKeepsRunningWork(t *testing.T) {
	l := NewLimiter(3)
	var releases []func()
	for i := 0; i < 3; i++ {
		r, ok := l.TryAcquire()
		if !ok {
			t.Fatal("acquire")
		}
		releases = append(releases, r)
	}
	l.SetLimit(1)
	if l.InFlight() != 3 {
		t.Fatal("lowering the limit must not evict running work")
	}
	if _, ok := l.TryAcquire(); ok {
		t.Fatal("new work must be refused until usage drops under the new limit")
	}
	for _, r := range releases {
		r()
	}
	if _, ok := l.TryAcquire(); !ok {
		t.Fatal("under the new limit acquisition works again")
	}
}

func TestLimiterIsSafeUnderConcurrency(t *testing.T) {
	l := NewLimiter(5)
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, ok := l.TryAcquire(); ok {
				mu.Lock()
				granted++
				mu.Unlock()
				r()
			}
		}()
	}
	wg.Wait()
	if l.InFlight() != 0 {
		t.Fatalf("in flight after all releases = %d", l.InFlight())
	}
	if granted == 0 {
		t.Fatal("some acquisitions must succeed")
	}
}

// ---- narrowing and tool sets ----

func TestNarrowPermissionModeNeverWidens(t *testing.T) {
	cases := []struct{ parent, requested, want string }{
		{"ask", "bypass", "ask"},
		{"ask", "", "ask"},
		{"accept_edits", "ask", "ask"},
		{"accept_edits", "bypass", "accept_edits"},
		{"bypass", "accept_edits", "accept_edits"},
		{"bypass", "", "bypass"},
		{"bypass", "bypass", "bypass"},
		{"", "bypass", "ask"},
	}
	for _, c := range cases {
		if got := NarrowPermissionMode(c.parent, c.requested); got != c.want {
			t.Errorf("Narrow(%q, %q) = %q, want %q", c.parent, c.requested, got, c.want)
		}
	}
}

func TestEffectiveToolsIntersectsEveryLayer(t *testing.T) {
	parent := []string{"read", "grep", "write", "run_command", "question", "spawn_agent", "context7__search", "context7__docs", "other__tool"}
	def := &Definition{Tools: []string{"read", "grep", "run_command", "context7__*", "question"}, DisallowedTools: []string{"grep"}}
	got := EffectiveTools(parent, nil, def, []string{"question", "spawn_agent"})
	want := []string{"context7__docs", "context7__search", "read", "run_command"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("effective = %v, want %v", got, want)
	}
	// A child mode set (plan) trims further, and nothing outside the parent set ever appears.
	got = EffectiveTools(parent, []string{"read", "glob", "grep", "run_command"}, def, nil)
	if strings.Join(got, ",") != "read,run_command" {
		t.Fatalf("mode-narrowed effective = %v", got)
	}
	// No allowlist means the whole parent set minus denies and exclusions.
	got = EffectiveTools(parent, nil, &Definition{DisallowedTools: []string{"write"}}, []string{"question"})
	if strings.Contains(strings.Join(got, ","), "write") || strings.Contains(strings.Join(got, ","), "question") {
		t.Fatalf("denylist and exclusions must apply without an allowlist: %v", got)
	}
	if len(got) != len(parent)-2 {
		t.Fatalf("unrestricted child keeps the rest of the parent set, got %v", got)
	}
}

func TestMatchToolPatterns(t *testing.T) {
	if !MatchTool("context7__*", "context7__search") || MatchTool("context7__*", "other__x") {
		t.Fatal("prefix patterns must match by prefix only")
	}
	if !MatchTool("read", "read") || MatchTool("read", "read_file") {
		t.Fatal("plain names match exactly")
	}
	if MatchTool("*", "anything") != true {
		t.Fatal("a bare star matches everything")
	}
}

func TestResolveTimeoutPrecedence(t *testing.T) {
	cases := []struct {
		call, def, expected, fallback, want int
	}{
		{120, 300, 10, 1800, 120},
		{0, 300, 10, 1800, 300},
		{0, 0, 10, 1800, 60},
		{0, 0, 100, 1800, 300},
		{0, 0, 0, 1800, 1800},
	}
	for _, c := range cases {
		if got := ResolveTimeoutSeconds(c.call, c.def, c.expected, c.fallback); got != c.want {
			t.Errorf("Resolve(%d,%d,%d,%d) = %d, want %d", c.call, c.def, c.expected, c.fallback, got, c.want)
		}
	}
}

// ---- catalog ----

func TestCatalogAndPromptBlockHideHiddenDefinitions(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeDef(t, cwd, ".coddy/agents/reviewer.md", reviewerDef)
	writeDef(t, cwd, ".coddy/agents/secret.md", "---\ndescription: hidden helper\nhidden: true\n---\nshh\n")
	store := NewTrustStore(home)
	defs := NewLoader([]string{"${CODDY_HOME}/agents", "${CWD}/.coddy/agents"}, "ask").Load(cwd, home)
	entries := BuildCatalog(defs, "ask", CanonicalWorkspace(cwd), store)

	var reviewer, secret *CatalogEntry
	for i := range entries {
		switch entries[i].Name {
		case "reviewer":
			reviewer = &entries[i]
		case "secret":
			secret = &entries[i]
		}
	}
	if reviewer == nil || reviewer.Trust != TrustNeedsApproval || reviewer.Scope != ScopeProject {
		t.Fatalf("reviewer entry = %+v", reviewer)
	}
	if secret == nil || !secret.Hidden {
		t.Fatalf("hidden definitions stay in the catalog with the flag: %+v", secret)
	}
	block := PromptBlock(entries)
	if !strings.Contains(block, "reviewer") || !strings.Contains(block, "Reviews a diff") {
		t.Fatalf("prompt block must name visible definitions: %q", block)
	}
	if strings.Contains(block, "secret") || strings.Contains(block, "hidden helper") {
		t.Fatalf("prompt block must not leak hidden definitions: %q", block)
	}
	if !strings.Contains(block, "needs approval") {
		t.Fatalf("prompt block must tell the model an unapproved project definition needs approval: %q", block)
	}
	var sb strings.Builder
	WriteListing(&sb, entries)
	listing := sb.String()
	for _, want := range []string{"reviewer", "project", "needs_approval", "secret", "hidden", "explore", "builtin"} {
		if !strings.Contains(listing, want) {
			t.Fatalf("listing missing %q:\n%s", want, listing)
		}
	}
}

func TestVisibleNamesForErrors(t *testing.T) {
	defs := []*Definition{{Name: "a"}, {Name: "b", Hidden: true}, {Name: "c"}}
	if got := strings.Join(VisibleNames(defs), ","); got != "a,c" {
		t.Fatalf("visible names = %q", got)
	}
}
