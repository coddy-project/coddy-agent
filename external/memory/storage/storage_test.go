//go:build memory

package memstorage

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The project notes sit in .coddy/memory of the session folder, beside the
// project's other Coddy files, never in a bare memory folder of the checkout.
func TestNewStoreRoots(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	work := filepath.Join(t.TempDir(), "work")
	custom := filepath.Join(t.TempDir(), "notes")
	cases := []struct {
		name        string
		mem         config.MemoryConfig
		cwd         string
		wantGlobal  string
		wantProject string
	}{
		{"defaults", config.MemoryConfig{}, work, filepath.Join(home, "memory"), filepath.Join(work, ".coddy", "memory")},
		{"memory.dir", config.MemoryConfig{Dir: custom}, work, custom, filepath.Join(work, ".coddy", "memory")},
		{"no cwd", config.MemoryConfig{}, "  ", filepath.Join(home, "memory"), filepath.Join(".coddy", "memory")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := NewStore(&tc.mem, config.Paths{Home: home}, tc.cwd)
			if err != nil {
				t.Fatal(err)
			}
			if st.GlobalRoot() != tc.wantGlobal {
				t.Errorf("global root = %q, want %q", st.GlobalRoot(), tc.wantGlobal)
			}
			if st.ProjectRoot() != tc.wantProject {
				t.Errorf("project root = %q, want %q", st.ProjectRoot(), tc.wantProject)
			}
			if got := ProjectRootFor(tc.cwd); got != tc.wantProject {
				t.Errorf("ProjectRootFor(%q) = %q, want %q", tc.cwd, got, tc.wantProject)
			}
		})
	}
}

func TestStoreSearch(t *testing.T) {
	tmp := t.TempDir()
	g := filepath.Join(tmp, "g")
	p := filepath.Join(tmp, "p")
	if err := os.MkdirAll(g, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g, "prefs.md"), []byte("User prefers tabs and Go modules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "proj.md"), []byte("This repo uses Makefile for build"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := NewWithRoots(g, p)
	hits, err := st.Search("tabs golang", "both", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 1 {
		t.Fatalf("expected hits, got %d", len(hits))
	}
}

func TestStoreWriteFlexibleNestedAndList(t *testing.T) {
	tmp := t.TempDir()
	g := filepath.Join(tmp, "g")
	p := filepath.Join(tmp, "p")
	st := NewWithRoots(g, p)

	written, err := st.WriteFlexible("global", "API", "design/auth-notes.md", "body line")
	if err != nil {
		t.Fatal(err)
	}
	if written != "global:design/auth-notes.md" {
		t.Fatalf("written %q", written)
	}
	nodes, err := st.ListOneLevel("global", "design")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range nodes {
		if n.Kind == "file" && n.Name == "auth-notes.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("list nodes %#v", nodes)
	}
	if err := st.Mkdir("global", "preferences"); err != nil {
		t.Fatal(err)
	}
	nodes2, err := st.ListOneLevel("global", "")
	if err != nil {
		t.Fatal(err)
	}
	okDir := false
	for _, n := range nodes2 {
		if n.Name == "preferences" && n.Kind == "dir" {
			okDir = true
		}
	}
	if !okDir {
		t.Fatalf("expected preferences dir %#v", nodes2)
	}
}

func TestStoreRejectTraversalDotDot(t *testing.T) {
	g := filepath.Join(t.TempDir(), "gonly")
	st := NewWithRoots(g, filepath.Join(t.TempDir(), "ponly"))
	if _, err := st.WriteFlexible("global", "x", "../evil.md", "y"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := st.Read("global:../x"); err == nil {
		t.Fatal("expected error")
	}
	if err := st.Mkdir("global", ".."); err == nil {
		t.Fatal("expected error")
	}
}

func TestDeleteRemovesDirectoryTree(t *testing.T) {
	tmp := t.TempDir()
	g := filepath.Join(tmp, "g")
	p := filepath.Join(tmp, "p")
	st := NewWithRoots(g, p)
	if err := st.Mkdir("global", "notes"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.WriteFlexible("global", "t", "notes/a.md", "body"); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete("global:notes"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(g, "notes")); !os.IsNotExist(err) {
		t.Fatalf("expected notes tree gone: %v", err)
	}
}

func TestDeleteRefusesScopeRoot(t *testing.T) {
	st := NewWithRoots(t.TempDir(), t.TempDir())
	for _, rel := range []string{"global:", "global:.", "project:", "project:."} {
		if err := st.Delete(rel); err == nil {
			t.Fatalf("expected error for %q", rel)
		}
	}
}

func TestSlugify(t *testing.T) {
	if g := slugify("  Hello World!!  "); g != "hello-world" {
		t.Fatalf("got %q", g)
	}
	if g := slugify(""); g != "note" {
		t.Fatalf("got %q", g)
	}
	// Titles in another script keep their letters instead of collapsing to "note".
	if g := slugify("Предпочтения пользователя"); g != "предпочтения-пользователя" {
		t.Fatalf("got %q", g)
	}
	if g := slugify(strings.Repeat("ж", 100)); utf8.RuneCountInString(g) != 80 || !utf8.ValidString(g) {
		t.Fatalf("long slug must be cut at 80 characters: %q", g)
	}
}

// Two notes saved without relative_path under Cyrillic titles land in two
// files: before the fix both became note.md and the second overwrote the first.
func TestWriteFlexibleKeepsNotesWithNonLatinTitlesApart(t *testing.T) {
	st := NewWithRoots(t.TempDir(), t.TempDir())
	p1, err := st.WriteFlexible("project", "Предпочтения", "", "first")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := st.WriteFlexible("project", "Архитектура", "", "second")
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatalf("both notes written to %s", p1)
	}
	if got, _ := st.Read(p1); strings.TrimSpace(got) != "first" {
		t.Fatalf("first note overwritten: %q", got)
	}
}

// Search snippets are cut in characters: a Cyrillic body keeps 1200 of them
// and stays valid UTF-8.
func TestSearchSnippetCutsByCharacters(t *testing.T) {
	st := NewWithRoots(t.TempDir(), t.TempDir())
	body := "кодди " + strings.Repeat("ж", 2000)
	if _, err := st.WriteFlexible("project", "n", "n.md", body); err != nil {
		t.Fatal(err)
	}
	hits, err := st.Search("кодди", "project", 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits=%v err=%v", hits, err)
	}
	snip := hits[0].Snippet
	if !utf8.ValidString(snip) {
		t.Fatal("snippet is not valid UTF-8")
	}
	if n := utf8.RuneCountInString(strings.TrimSuffix(snip, "\n...")); n != 1200 {
		t.Fatalf("snippet keeps %d characters, want 1200", n)
	}
}

// extractMemoryLinkTargets finds scope:relative references suitable for coddy_memory_read.
func extractMemoryLinkTargets(body string) []string {
	raw := regexp.MustCompile(`\b(global|project):([a-zA-Z0-9_./\-]+\.(?:md|txt))\b`)
	mdHref := regexp.MustCompile(`\[[^\]]*]\(((?:global|project):[a-zA-Z0-9_./\-]+\.(?:md|txt))\)`)
	var out []string
	seen := make(map[string]struct{})
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		if strings.Contains(s, "..") {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, sm := range raw.FindAllStringSubmatch(body, -1) {
		add(sm[1] + ":" + sm[2])
	}
	for _, sm := range mdHref.FindAllStringSubmatch(body, -1) {
		add(strings.Trim(sm[1], `"'`))
	}
	return out
}

func sliceContains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func memoryTreeFixture(t *testing.T, globalRoot string) {
	t.Helper()
	layout := []struct {
		relPath string
		body    string
	}{
		{
			"index.md",
			"# Coddy memory hub\n\nStart here after recall search for hub navigation.\n\nSee [architecture index](global:docs/arch/overview.md).\nBare link global:guides/quickstart.txt for plain references.\n",
		},
		{
			"guides/quickstart.txt",
			"Quick checklist. Dive into global:topics/services/api-map.md.\n",
		},
		{
			"docs/arch/overview.md",
			"## Services overview\n\nHigh-level sketch. Related [API map](global:topics/services/api-map.md).\n",
		},
		{
			"topics/services/api-map.md",
			"Routes table. Secrets live in vault note: [vault](global:topics/secrets/vault.md).\n",
		},
		{
			"topics/secrets/vault.md",
			"## Vault naming\nANSWER_UNIQUE_TOKEN_XYZZY_42\nStored only here; indexing pages must not duplicate this literal.\n",
		},
	}
	for _, f := range layout {
		dir := filepath.Dir(f.relPath)
		if dir != "." {
			fullDir := filepath.Join(globalRoot, dir)
			if err := os.MkdirAll(fullDir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		p := filepath.Join(globalRoot, f.relPath)
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func simulateLinkWalkRecall(st *Store, seeds []string, maxReads int, wantSubstring string) (pathOrder []string, found bool, err error) {
	queued := append([]string(nil), seeds...)
	sort.Strings(queued)
	seenRead := make(map[string]bool)
	readCount := 0
	var blobs []string
	for len(queued) > 0 && readCount < maxReads {
		p := queued[0]
		queued = queued[1:]
		if seenRead[p] {
			continue
		}
		seenRead[p] = true
		body, readErr := st.Read(p)
		if readErr != nil {
			return pathOrder, false, readErr
		}
		readCount++
		pathOrder = append(pathOrder, p)
		blobs = append(blobs, body)
		if strings.Contains(body, wantSubstring) {
			return pathOrder, true, nil
		}
		for _, tgt := range extractMemoryLinkTargets(body) {
			if seenRead[tgt] {
				continue
			}
			queued = append(queued, tgt)
		}
		sort.Strings(queued)
	}
	found = strings.Contains(strings.Join(blobs, "\n"), wantSubstring)
	return pathOrder, found, nil
}

func TestExtractMemoryLinkTargets(t *testing.T) {
	body := "[a](global:docs/x.md) and bare global:guides/y.txt.\nAlso [b](global:topics/z.md) tail.\n"
	got := extractMemoryLinkTargets(body)
	want := []string{"global:docs/x.md", "global:guides/y.txt", "global:topics/z.md"}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("idx %d got %q want %q", i, got[i], want[i])
		}
	}
	if extract := extractMemoryLinkTargets(`bad global:../x.md skip`); len(extract) != 0 {
		t.Fatalf("expected skip .. paths, got %v", extract)
	}
}

func TestSearchBootstrapsTreeEntryThenLinkedWalkFindsBuriedLeaf(t *testing.T) {
	const secret = "ANSWER_UNIQUE_TOKEN_XYZZY_42"
	tmp := t.TempDir()
	g := filepath.Join(tmp, "memglobal")
	proj := filepath.Join(tmp, "memproj")
	if err := os.MkdirAll(g, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	memoryTreeFixture(t, g)
	st := NewWithRoots(g, proj)

	hits, err := st.Search("hub navigation coddy start recall", "global", 10)
	if err != nil {
		t.Fatal(err)
	}
	seeds := make([]string, 0, len(hits))
	for _, h := range hits {
		seeds = append(seeds, h.Path)
	}
	if len(seeds) == 0 {
		t.Fatal("search returned no bootstrap hits")
	}
	if hits[0].Path != "global:index.md" {
		t.Fatalf("expected index as top hit for bootstrap query, got %v", hits[0])
	}

	order, ok, err := simulateLinkWalkRecall(st, seeds, 20, secret)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("never found secret after walking links; reads=%v", order)
	}
	if !sliceContains(order, "global:index.md") {
		t.Fatalf("expected index in walk order, got %v", order)
	}
	last := order[len(order)-1]
	if last != "global:topics/secrets/vault.md" {
		t.Fatalf("want final read at vault leaf, got %q order=%v", last, order)
	}
}

func TestSearchIntermediateQueryThenWalkReachesVault(t *testing.T) {
	const secret = "ANSWER_UNIQUE_TOKEN_XYZZY_42"
	tmp := t.TempDir()
	g := filepath.Join(tmp, "g")
	p := filepath.Join(tmp, "p")
	if err := os.MkdirAll(g, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	memoryTreeFixture(t, g)
	st := NewWithRoots(g, p)

	firstHits, err := st.Search("services overview routes sketch", "global", 5)
	if err != nil {
		t.Fatal(err)
	}
	seeds := make([]string, 0, len(firstHits))
	for _, h := range firstHits {
		seeds = append(seeds, h.Path)
	}
	order, ok, err := simulateLinkWalkRecall(st, seeds, 20, secret)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("linked walk failed from intermediate search seeds")
	}
	if !sliceContains(order, "global:topics/services/api-map.md") {
		t.Fatalf("expected api-map in pathOrder=%v", order)
	}
}

// writeNotes saves rel -> body pairs under one scope of the store.
func writeNotes(t *testing.T, st *Store, scope string, notes map[string]string) {
	t.Helper()
	for rel, body := range notes {
		if _, err := st.WriteFlexible(scope, "n", rel, body); err != nil {
			t.Fatal(err)
		}
	}
}

// searchPaths is the paths a search returns, best first.
func searchPaths(t *testing.T, st *Store, query, scope string, maxHits int) []string {
	t.Helper()
	hits, err := st.Search(query, scope, maxHits)
	if err != nil {
		t.Fatalf("Search(%q, %q): %v", query, scope, err)
	}
	paths := make([]string, 0, len(hits))
	for _, h := range hits {
		paths = append(paths, h.Path)
	}
	return paths
}

func equalPaths(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A question's function words count for nothing: the generic how-to note
// that shares "how to the" with it is not a hit at all, and the note about
// deploying the relay comes first (issue #698).
func TestSearchRanksTheNoteTheQuestionIsAbout(t *testing.T) {
	st := NewWithRoots(t.TempDir(), t.TempDir())
	writeNotes(t, st, "global", map[string]string{
		"how-to-write-a-note.md": "How to write a note. Open the file, add a heading and save it to the folder.",
		"relay-deployment.md":    "Relay deployment: build the image and restart the relay service.",
		"relay-steps.md":         "relay deploy steps",
	})
	got := searchPaths(t, st, "how to deploy the relay", "global", 8)
	want := []string{"global:relay-deployment.md", "global:relay-steps.md"}
	if !equalPaths(got, want) {
		t.Fatalf("hits = %v, want %v", got, want)
	}
}

// A plural finds its singular and the other way round; a query word also
// finds the words it begins.
func TestSearchStemsAndMatchesPrefixes(t *testing.T) {
	st := NewWithRoots(t.TempDir(), t.TempDir())
	writeNotes(t, st, "global", map[string]string{
		"a.md": "the config file lives next to the binary",
		"b.md": "tabs and spaces are both fine",
		"c.md": "deployment happens on friday",
	})
	for _, tc := range []struct{ query, want string }{
		{"configs", "global:a.md"},
		{"tab", "global:b.md"},
		{"deploy", "global:c.md"},
	} {
		if got := searchPaths(t, st, tc.query, "global", 8); !equalPaths(got, []string{tc.want}) {
			t.Errorf("Search(%q) = %v, want [%s]", tc.query, got, tc.want)
		}
	}
}

// A Russian query finds a note that has another form of its word.
func TestSearchMatchesAnInflectedRussianWord(t *testing.T) {
	st := NewWithRoots(t.TempDir(), t.TempDir())
	writeNotes(t, st, "project", map[string]string{
		"relay.md": "Настройки релея хранятся в файле конфигурации узла.",
		"misc.md":  "Список покупок на неделю.",
	})
	if got := searchPaths(t, st, "настройка файл", "project", 8); !equalPaths(got, []string{"project:relay.md"}) {
		t.Fatalf("hits = %v", got)
	}
}

// A word few notes have outweighs one most notes have, even when the common
// word repeats: counting the words a note shares with the query would tie
// them and leave the order to the path.
func TestSearchRareWordOutranksACommonOne(t *testing.T) {
	st := NewWithRoots(t.TempDir(), t.TempDir())
	notes := map[string]string{
		"a-common.md": "service service service restarts after every deploy",
		"z-rare.md":   "zebra restarts after every deploy of anything",
	}
	for _, n := range []string{"b", "c", "d", "e"} {
		notes[n+".md"] = "the service runs on a schedule and restarts nightly"
	}
	writeNotes(t, st, "global", notes)
	got := searchPaths(t, st, "service zebra", "global", 8)
	if len(got) == 0 || got[0] != "global:z-rare.md" {
		t.Fatalf("hits = %v, want global:z-rare.md first", got)
	}
}

// A word in the file name or a folder weighs more than the same word in the
// body.
func TestSearchPathWordsOutweighTheBody(t *testing.T) {
	st := NewWithRoots(t.TempDir(), t.TempDir())
	writeNotes(t, st, "global", map[string]string{
		"misc.md":              "relay mentioned here once among other words",
		"relay.md":             "nothing mentioned here once among other words",
		"deploy/checklist.md":  "steps mentioned here once among other words",
		"unrelated/storage.md": "nothing mentioned here once among other words",
	})
	got := searchPaths(t, st, "relay", "global", 8)
	if len(got) != 2 || got[0] != "global:relay.md" || got[1] != "global:misc.md" {
		t.Fatalf("file name vs body: hits = %v", got)
	}
	got = searchPaths(t, st, "deploy", "global", 8)
	if len(got) != 1 || got[0] != "global:deploy/checklist.md" {
		t.Fatalf("folder name: hits = %v", got)
	}
}

// The AGENTS.md at the root of a scope is the guide the child already has at
// the top of its task, so it is never a hit; one in a subfolder is an
// ordinary note.
func TestSearchLeavesTheRootGuideOut(t *testing.T) {
	st := NewWithRoots(t.TempDir(), filepath.Join(t.TempDir(), ".coddy", "memory"))
	writeNotes(t, st, "global", map[string]string{
		"AGENTS.md":         "Guide: relay deploy notes live under ops.",
		"ops/AGENTS.md":     "relay deploy conventions of the ops folder",
		"ops/deployment.md": "relay deploy: build, push, restart",
	})
	writeNotes(t, st, "project", map[string]string{"AGENTS.md": "Guide of the project: relay deploy."})
	got := searchPaths(t, st, "relay deploy", "both", 8)
	for _, p := range got {
		if p == "global:AGENTS.md" || p == "project:AGENTS.md" {
			t.Fatalf("the root guide is a hit: %v", got)
		}
	}
	if len(got) != 2 || !sliceContains(got, "global:ops/AGENTS.md") || !sliceContains(got, "global:ops/deployment.md") {
		t.Fatalf("hits = %v, want the two notes of ops", got)
	}
	if got := searchPaths(t, st, "guide", "both", 8); len(got) != 0 {
		t.Fatalf("a word only the guides have found %v", got)
	}
}

// Behaviour that does not depend on the ranking: what an empty or empty-
// handed query returns, which roots a scope reads, how many hits come back
// and how equal scores are ordered.
func TestSearchQueriesScopesAndLimits(t *testing.T) {
	st := NewWithRoots(t.TempDir(), filepath.Join(t.TempDir(), ".coddy", "memory"))
	writeNotes(t, st, "global", map[string]string{"g.md": "shared topic of the global notes"})
	writeNotes(t, st, "project", map[string]string{"p.md": "shared topic of the project notes"})
	for _, q := range []string{"", "   ", "zzzqqq", "?!"} {
		if got := searchPaths(t, st, q, "both", 8); len(got) != 0 {
			t.Errorf("Search(%q) = %v, want nothing", q, got)
		}
	}
	for scope, want := range map[string][]string{
		"global":   {"global:g.md"},
		"project":  {"project:p.md"},
		" Global":  {"global:g.md"},
		"both":     {"global:g.md", "project:p.md"},
		"":         {"global:g.md", "project:p.md"},
		"nonsense": {"global:g.md", "project:p.md"},
	} {
		if got := searchPaths(t, st, "shared topic", scope, 8); !equalPaths(got, want) {
			t.Errorf("scope %q: hits = %v, want %v", scope, got, want)
		}
	}
	if got := searchPaths(t, st, "shared topic", "both", 1); len(got) != 1 {
		t.Errorf("maxHits 1 returned %v", got)
	}
	many := map[string]string{}
	for i := 0; i < 12; i++ {
		many[fmt.Sprintf("n%02d.md", i)] = "identical words here"
	}
	writeNotes(t, st, "global", many)
	got := searchPaths(t, st, "identical words", "global", 0)
	if len(got) != 8 {
		t.Fatalf("the default limit returned %d hits, want 8", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("equal scores are not ordered by path: %v", got)
		}
	}
}

// A hit carries its score, best first.
func TestSearchScoresDescend(t *testing.T) {
	st := NewWithRoots(t.TempDir(), t.TempDir())
	writeNotes(t, st, "global", map[string]string{
		"relay.md": "relay deploy",
		"misc.md":  "some words about a relay and other things entirely",
	})
	hits, err := st.Search("relay deploy", "global", 8)
	if err != nil || len(hits) != 2 {
		t.Fatalf("hits=%v err=%v", hits, err)
	}
	if hits[0].Score <= 0 || hits[0].Score <= hits[1].Score {
		t.Fatalf("scores %v then %v", hits[0].Score, hits[1].Score)
	}
}
