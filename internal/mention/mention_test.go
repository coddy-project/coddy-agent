package mention

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- grammar ---
// The cases live in testdata/grammar_cases.json, which
// external/ui/src/ui/skills/draftAt.test.ts reads too: both twins of the
// grammar are held to the same literals. A token is its span (the text a
// surface highlights) and its readings, longest first: a path, a path with
// "#start-end" for a range, "session:<id>" (or rule, agent) for a meta
// reference, "url:<address>" for a web page.

type grammarCase struct {
	In     string `json:"in"`
	Tokens []struct {
		Span     string   `json:"span"`
		Readings []string `json:"readings"`
	} `json:"tokens"`
}

func readingsOf(tk Token) []string {
	switch {
	case tk.URL != "":
		return []string{"url:" + tk.URL}
	case !tk.IsPath():
		return []string{string(tk.Scheme) + ":" + tk.Ref}
	}
	var rs []string
	for _, r := range tk.Readings {
		s := r.Path
		if !r.Range.IsZero() {
			s += "#" + rangeKey(r.Range)
		}
		rs = append(rs, s)
	}
	return rs
}

func TestParseSharedCases(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "grammar_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []grammarCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 30 {
		t.Fatalf("only %d shared cases", len(cases))
	}
	for _, tc := range cases {
		toks := Parse(tc.In)
		if len(toks) != len(tc.Tokens) {
			t.Errorf("Parse(%q): %d tokens, want %d", tc.In, len(toks), len(tc.Tokens))
			continue
		}
		for i, tk := range toks {
			if got := tc.In[tk.Start:tk.End]; got != tc.Tokens[i].Span {
				t.Errorf("Parse(%q) token %d span %q, want %q", tc.In, i, got, tc.Tokens[i].Span)
			}
			if got := readingsOf(tk); !reflect.DeepEqual(got, tc.Tokens[i].Readings) {
				t.Errorf("Parse(%q) token %d readings %q, want %q", tc.In, i, got, tc.Tokens[i].Readings)
			}
		}
	}
}

func TestParseSpans(t *testing.T) {
	text := "see @Dockerfile:21-31 and @session:sess_1."
	toks := Parse(text)
	if len(toks) != 2 {
		t.Fatalf("tokens = %+v", toks)
	}
	if got := text[toks[0].Start:toks[0].End]; got != "@Dockerfile:21-31" {
		t.Fatalf("first span %q", got)
	}
	if got := text[toks[1].Start:toks[1].End]; got != "@session:sess_1" {
		t.Fatalf("second span %q", got)
	}
}

// --- paths ---

func TestResolveForms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX spellings")
	}
	cwd := t.TempDir()
	home := t.TempDir()
	cases := []struct {
		typed, abs, display string
		inside              bool
	}{
		{"a/b.go", filepath.Join(cwd, "a/b.go"), "a/b.go", true},
		{"./a/b.go", filepath.Join(cwd, "a/b.go"), "a/b.go", true},
		{"./", cwd, "./", true},
		{filepath.Join(cwd, "x.go"), filepath.Join(cwd, "x.go"), "x.go", true},
		{"~/notes.md", filepath.Join(home, "notes.md"), filepath.Join(home, "notes.md"), false},
		{"../up.txt", filepath.Join(filepath.Dir(cwd), "up.txt"), filepath.Join(filepath.Dir(cwd), "up.txt"), false},
		{"file://" + filepath.Join(cwd, "u.go"), filepath.Join(cwd, "u.go"), "u.go", true},
		{"file://localhost" + filepath.Join(cwd, "u.go"), filepath.Join(cwd, "u.go"), "u.go", true},
		{"/etc/hosts", "/etc/hosts", "/etc/hosts", false},
	}
	for _, tc := range cases {
		loc, ok := Resolve(cwd, home, tc.typed)
		if !ok {
			t.Fatalf("Resolve(%q) failed", tc.typed)
		}
		if loc.Abs != tc.abs || loc.Display != tc.display || loc.Inside != tc.inside {
			t.Errorf("Resolve(%q) = %+v, want abs %q display %q inside %v", tc.typed, loc, tc.abs, tc.display, tc.inside)
		}
	}
	if _, ok := Resolve(cwd, "", "~/x"); ok {
		t.Fatal("~ without a home must not resolve")
	}
	// A share on another host is not the local file of the same path.
	if loc, ok := Resolve(cwd, home, "file://server/etc/hosts"); ok {
		t.Fatalf("a file URI naming another host resolved to %q", loc.Abs)
	}
}

func TestResolveThroughSymlinkedWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "ws")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "f.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	realResolved, _ := filepath.EvalSymlinks(real)
	loc, ok := Resolve(link, "", filepath.Join(realResolved, "f.go"))
	if !ok || !loc.Inside || loc.Display != "f.go" {
		t.Fatalf("physical spelling of a symlinked workspace: %+v", loc)
	}
}

// --- ranking ---

func TestRankPrefersNameOverPath(t *testing.T) {
	paths := []string{
		"docs/assets/app-shot.png",
		"external/cli/app.go",
		"external/cli/app_test.go",
		"internal/agent/react.go",
		"app.go",
		"external/ui/src/ui/App.tsx",
	}
	got, total := Rank("app", paths, func(s string) string { return s }, 3)
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	var names []string
	for _, r := range got {
		names = append(names, r.Item)
	}
	want := []string{"app.go", "external/cli/app.go", "external/ui/src/ui/App.tsx"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("ranked %q, want %q", names, want)
	}
}

func TestRankFindsAFileDeepInALargeTree(t *testing.T) {
	var paths []string
	for i := 0; i < 5000; i++ {
		paths = append(paths, filepath.ToSlash(filepath.Join("pkg", "mod"+itoa(i), "file.go")))
	}
	paths = append(paths, "zz/deep/nested/target_handler.go")
	got, total := Rank("targhand", paths, func(s string) string { return s }, 50)
	if total != 1 || got[0].Item != "zz/deep/nested/target_handler.go" {
		t.Fatalf("got %+v total %d", got, total)
	}
	got, _ = Rank("nested/target", paths, func(s string) string { return s }, 5)
	if len(got) == 0 || got[0].Item != "zz/deep/nested/target_handler.go" {
		t.Fatalf("path word: %+v", got)
	}
	got, _ = Rank("handler deep", paths, func(s string) string { return s }, 5)
	if len(got) != 1 {
		t.Fatalf("two words must both match: %+v", got)
	}
}

// --- index ---

func TestBuildIndexHonoursGitignoreAndKeepsDotfiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "build/\n")
	write("src/main.go", "package main")
	write(".github/workflows/ci.yml", "on: push")
	write("build/out.bin", "x")
	write("gone.txt", "x")
	run("add", ".")
	if err := os.Remove(filepath.Join(root, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	entries, source, _, err := BuildIndex(context.Background(), root, 100)
	if err != nil || source != SourceGit {
		t.Fatalf("source %q err %v", source, err)
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.Path] = true
	}
	for _, want := range []string{"src/", "src/main.go", ".github/workflows/ci.yml", ".gitignore"} {
		if !have[want] {
			t.Errorf("missing %q in %v", want, entries)
		}
	}
	for _, not := range []string{"build/out.bin", "build/", "gone.txt"} {
		if have[not] {
			t.Errorf("unexpected %q", not)
		}
	}
}

func TestBuildIndexWalksOutsideGit(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"a/b.go", "node_modules/x/y.js", ".hidden/c.txt", "d.md"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("x"), 0o644)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
	entries, source, truncated, err := BuildIndex(context.Background(), root, 100)
	if err != nil || source != SourceWalk || truncated {
		t.Fatalf("source %q truncated %v err %v", source, truncated, err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Path)
	}
	want := []string{"a/", "a/b.go", "d.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("walk = %q, want %q", got, want)
	}
	_, _, truncated, _ = BuildIndex(context.Background(), root, 2)
	if !truncated {
		t.Fatal("a capped walk must say it was cut")
	}
}

func TestIndexCacheServesStaleAndRebuilds(t *testing.T) {
	var builds atomic.Int32
	var clock atomic.Int64
	c := NewIndexCache()
	c.Now = func() time.Time { return time.Unix(0, clock.Load()) }
	c.Build = func(_ context.Context, root string) ([]Entry, string, bool, error) {
		n := builds.Add(1)
		return []Entry{{Path: "v" + itoa(int(n)) + ".go"}}, SourceWalk, false, nil
	}
	landed := make(chan string, 8)
	defer c.Subscribe(func(root string) { landed <- root })()

	first := c.Get("/ws", time.Second)
	if len(first.Entries) != 1 || first.Entries[0].Path != "v1.go" {
		t.Fatalf("first build: %+v", first)
	}
	<-landed
	// Fresh: no rebuild.
	if again := c.Get("/ws", 0); again.Entries[0].Path != "v1.go" || builds.Load() != 1 {
		t.Fatalf("fresh read rebuilt: %+v builds %d", again, builds.Load())
	}
	// A forced refresh serves the old list at once and lands the new one.
	stale := c.Refresh("/ws", 0)
	if stale.Entries[0].Path != "v1.go" {
		t.Fatalf("refresh must serve the last build: %+v", stale)
	}
	select {
	case <-landed:
	case <-time.After(2 * time.Second):
		t.Fatal("rebuild never landed")
	}
	if got := c.Get("/ws", 0); got.Entries[0].Path != "v2.go" {
		t.Fatalf("after rebuild: %+v", got)
	}
	// Past its time to live the next read starts a rebuild on its own.
	clock.Add(int64(MinIndexTTL) + 1)
	_ = c.Get("/ws", 0)
	select {
	case <-landed:
	case <-time.After(2 * time.Second):
		t.Fatal("stale read did not rebuild")
	}
	if builds.Load() != 3 {
		t.Fatalf("builds = %d, want 3", builds.Load())
	}
}

func TestSnapshotUnder(t *testing.T) {
	s := Snapshot{Entries: withDirs([]string{"a/b/c.go", "a/d.go", "e.go"}, 100)}
	var got []string
	for _, e := range s.Under("a/") {
		got = append(got, e.Path)
	}
	want := []string{"a/b/", "a/b/c.go", "a/d.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Under = %q, want %q", got, want)
	}
}

// --- attachments ---

func TestAttachmentXMLRoundTrip(t *testing.T) {
	a := Attachment{Path: "/home/u/notes.md", Typed: "~/notes.md", Lines: Range{Start: 2, End: 3}, Body: "x ]]> y </coddy_attachment> z"}
	xmlText := a.XML()
	if !strings.Contains(xmlText, `mention="~/notes.md"`) || !strings.Contains(xmlText, `lines="2-3"`) {
		t.Fatalf("attributes: %s", xmlText)
	}
	msg := "look at @~/notes.md:2-3\n\n" + xmlText + "\n\n" + Attachment{Kind: KindDirectory, Path: "src/", Body: "src/a.go"}.XML()
	blocks := Blocks(msg)
	if len(blocks) != 2 || blocks[0].Typed != "~/notes.md" || blocks[0].Lines != (Range{2, 3}) || blocks[1].Path != "src/" {
		t.Fatalf("blocks: %+v", blocks)
	}
	if got := ForDisplay(msg); got != "look at @~/notes.md:2-3\n\n@src/" {
		t.Fatalf("display: %q", got)
	}
	// A typed web page is not shown a second time under the text.
	page := "read @https://x.dev/a please\n\n" + Attachment{Kind: KindURL, Path: "https://x.dev/a", Body: "# A"}.XML()
	if got := ForDisplay(page); got != "read @https://x.dev/a please" {
		t.Fatalf("url display: %q", got)
	}
}

// Piped stdin is data with no mention in the text: the model reads it as a
// kind="stdin" element, and a transcript shows a label in its place, never an
// "@stdin" that would read as a file of that name.
func TestStdinAttachmentXMLAndDisplay(t *testing.T) {
	el := Attachment{Kind: KindStdin, Path: "stdin", Body: "diff --git a/x b/x\n+@secret.txt\n"}.XML()
	if !strings.Contains(el, `kind="stdin"`) || !strings.Contains(el, `path="stdin"`) {
		t.Fatalf("element: %s", el)
	}
	msg := "Review this change\n\n" + el
	blocks := Blocks(msg)
	if len(blocks) != 1 || blocks[0].Kind != KindStdin {
		t.Fatalf("blocks: %+v", blocks)
	}
	if got := ForDisplay(msg); got != "Review this change\n\n"+StdinLabel {
		t.Fatalf("display: %q", got)
	}
}
