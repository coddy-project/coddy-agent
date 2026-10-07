//go:build memory

package memstorage

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

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
