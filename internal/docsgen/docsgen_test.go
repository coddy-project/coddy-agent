package docsgen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlugMatchesGitHubAnchors(t *testing.T) {
	cases := map[string]string{
		"Quick start":                              "quick-start",
		"**`TAGS` vs `go build -tags`**":           "tags-vs-go-build--tags",
		"Dry run: probing what the file points at": "dry-run-probing-what-the-file-points-at",
		"Paths (`CODDY_HOME`, `CODDY_CWD`)":        "paths-coddy_home-coddy_cwd",
		"`session/new`":                            "sessionnew",
		"Настройка":                                "настройка",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSpliceReplacesOnlyTheBlock(t *testing.T) {
	doc := "intro\n<!-- docsgen:nav:start -->\nold\n<!-- docsgen:nav:end -->\noutro\n"
	got, err := Splice(doc, MarkerNav, "new body")
	if err != nil {
		t.Fatal(err)
	}
	want := "intro\n<!-- docsgen:nav:start -->\nnew body\n<!-- docsgen:nav:end -->\noutro\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, err := Splice("no markers", MarkerNav, "x"); err == nil {
		t.Fatal("expected an error without markers")
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckLinksFindsBrokenTargetsAndAnchors(t *testing.T) {
	root := t.TempDir()
	write(t, root, "docs/a.md", "# A\n\n## Some Heading\n\nSee [b](b.md), [anchor](b.md#other-heading), [bad](b.md#missing), [gone](nope.md), [img](assets/x.png), [ext](https://example.com), [self](#some-heading), [self-bad](#nowhere), `[code](inline.md)` and [ref][r].\n\n[r]: b.md\n[dead]: missing.md\n\n```\n[not a link](fenced.md)\n```\n\n~~~\n[not a link](tilde.md)\n~~~\n")
	write(t, root, "docs/b.md", "# B\n\nProse with inline ``` fences does not open a block.\n\n### Other heading\n")
	write(t, root, "docs/assets/x.png", "png")
	problems := CheckLinks(root, []string{"docs/a.md"})
	var msgs []string
	for _, p := range problems {
		msgs = append(msgs, p.Message)
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{"broken link nope.md", "#missing", "#nowhere", "broken link missing.md"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %v", want, problems)
		}
	}
	if len(problems) != 4 {
		t.Fatalf("unexpected problems: %v", problems)
	}
}

func TestCodeFencesAndAssetSizesWithCRLF(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("%q", eol), func(t *testing.T) {
			root := t.TempDir()
			body := "# Real\n\n~~~\n# Fake\n[example](missing.md)\n~~~\n\n[real](#real)\n"
			write(t, root, "docs/page.md", strings.ReplaceAll(body, "\n", eol))
			if problems := CheckLinks(root, []string{"docs/page.md"}); len(problems) != 0 {
				t.Fatalf("fenced example treated as a link: %v", problems)
			}
			if headingAnchors(filepath.Join(root, "docs/page.md"))["fake"] {
				t.Fatal("fenced heading became a page anchor")
			}
			svg := "<svg>\n</svg>\n"
			write(t, root, "docs/assets/icon.svg", strings.ReplaceAll(svg, "\n", eol))
			write(t, root, "docs/assets/INDEX.md", "Icon: `icon.svg`\n")
			assets, err := AssetInventory(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(assets) != 1 || assets[0].Size != int64(len(svg)) {
				t.Fatalf("inventory depends on checkout line endings: %+v", assets)
			}
		})
	}
}

func TestStaleIgnoresCheckoutLineEndings(t *testing.T) {
	root := t.TempDir()
	want := "# Page\n\nGenerated content.\n"
	r := Result{Files: map[string]string{"docs/page.md": want}}
	write(t, root, "docs/page.md", strings.ReplaceAll(want, "\n", "\r\n"))
	if problems := r.Stale(root); len(problems) != 0 {
		t.Fatalf("CRLF checkout reported as stale: %v", problems)
	}
	write(t, root, "docs/page.md", "# Page\r\n\r\nOld content.\r\n")
	if problems := r.Stale(root); len(problems) != 1 {
		t.Fatalf("real content change must still be reported: %v", problems)
	}
}

func TestCheckNavListsEveryPageOnce(t *testing.T) {
	root := t.TempDir()
	write(t, root, NavFile, "groups:\n  - id: g\n    title: G\n    pages:\n      - path: one.md\n        title: One\n        summary: s\n      - path: missing.md\n        title: Missing\n        summary: s\n")
	write(t, root, "docs/one.md", "# One\n")
	write(t, root, "docs/orphan.md", "# Orphan\n")
	write(t, root, "docs/noheading.md", "text\n")
	nav, err := LoadNav(root)
	if err != nil {
		t.Fatal(err)
	}
	problems := CheckNav(root, nav)
	want := []string{"page missing.md does not exist", "not listed in docs/nav.yaml"}
	for _, w := range want {
		found := false
		for _, p := range problems {
			if strings.Contains(p.Message, w) {
				found = true
			}
		}
		if !found {
			t.Errorf("missing problem %q in %v", w, problems)
		}
	}
}

// initGitRepo makes root a repository, so the walk has someone to ask what is
// ignored.
func initGitRepo(root string) error {
	cmd := exec.Command("git", "init")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init: %w\n%s", err, out)
	}
	return nil
}

func gitInit(t *testing.T, root string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if err := initGitRepo(root); err != nil {
		t.Skip(err)
	}
}

func TestDocsMarkdownSkipsWhatGitIgnores(t *testing.T) {
	// Neutralise the developer's own git configuration: the answer must come
	// from the .gitignore written below and from nothing else.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, ".gitignore", "docs/superpowers/\ndocs/scratch.md\n")
	write(t, root, NavFile, "groups:\n  - id: g\n    title: G\n    pages:\n      - path: one.md\n        title: One\n        summary: s\n")
	write(t, root, "docs/one.md", "# One\n")
	write(t, root, "docs/orphan.md", "# Orphan\n")
	write(t, root, "docs/plans/design.md", "# Design\n")
	write(t, root, "docs/scratch.md", "# Scratch\n")
	write(t, root, "docs/superpowers/plans/scratch.md", "# Scratch\n\n[gone](nowhere.md)\n")

	files, err := DocsMarkdown(root, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docs/one.md", "docs/orphan.md", "docs/plans/design.md"}
	if strings.Join(files, " ") != strings.Join(want, " ") {
		t.Fatalf("DocsMarkdown = %v, want %v", files, want)
	}

	nav, err := LoadNav(root)
	if err != nil {
		t.Fatal(err)
	}
	// The design records under docs/plans keep leaving the map through their
	// own rule, and only the page that really is missing from it is reported.
	problems := CheckNav(root, nav)
	if len(problems) != 1 || problems[0].File != "docs/orphan.md" {
		t.Fatalf("problems = %v, want only docs/orphan.md", problems)
	}
}

func TestDocsMarkdownWithoutGitListsEverything(t *testing.T) {
	root := t.TempDir()
	// Without a repository around it the walk cannot ask anyone what is
	// ignored, and it must carry on rather than fail.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
	write(t, root, ".gitignore", "docs/superpowers/\n")
	write(t, root, "docs/one.md", "# One\n")
	write(t, root, "docs/superpowers/plans/scratch.md", "# Scratch\n")

	files, err := DocsMarkdown(root, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docs/one.md", "docs/superpowers/plans/scratch.md"}
	if strings.Join(files, " ") != strings.Join(want, " ") {
		t.Fatalf("DocsMarkdown = %v, want %v", files, want)
	}
}

func TestConfigReferenceRendersTypesDefaultsAndItems(t *testing.T) {
	schema := `{"type":"object","properties":{
	  "logger":{"type":"object","description":"Logging.","properties":{
	    "level":{"type":"string","enum":["debug","info"],"description":"Verbosity."},
	    "outputs":{"type":"array","items":{"type":"string"},"description":"Sinks."}}},
	  "providers":{"type":"array","description":"Backends.","items":{"type":"object","properties":{
	    "name":{"type":"string","description":"Name."},
	    "timeout":{"type":["integer","null"],"default":30,"description":"Seconds."}}}},
	  "agent":{"type":"object","properties":{"max_turns":{"type":"integer","description":"Cap."}}}}}`
	defaults := map[string]string{"logger.level": "info", "agent.max_turns": "35"}
	out, err := ConfigReference([]byte(schema), defaults)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"### `providers`",
		"| `providers` | list of objects |  | Backends. |",
		"| `providers[].timeout` | integer or null | 30 | Seconds. |",
		"| `logger.level` | string, one of `debug`, `info` | info | Verbosity. |",
		"| `logger.outputs` | list of strings |  | Sinks. |",
		"| `agent.max_turns` | integer | 35 | Cap. |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "### `providers`") > strings.Index(out, "### `agent`") {
		t.Errorf("sections out of order:\n%s", out)
	}
}

func TestFlattenDefaultsSkipsEmptyAndObjectLists(t *testing.T) {
	type inner struct {
		Level   string   `yaml:"level"`
		Outputs []string `yaml:"outputs"`
		Count   int      `yaml:"count"`
		On      bool     `yaml:"on"`
	}
	type cfg struct {
		Logger inner               `yaml:"logger"`
		Items  []map[string]string `yaml:"items"`
	}
	got, err := FlattenDefaults(cfg{Logger: inner{Level: "info", Outputs: []string{"stderr", "file"}, On: true}, Items: []map[string]string{{"a": "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"logger.level": "info", "logger.outputs": "[stderr, file]", "logger.on": "true"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestSiteSlugsAndRedirectScript(t *testing.T) {
	if got := SiteSlug("getting-started/install.md"); got != "getting-started/install" {
		t.Errorf("slug = %q", got)
	}
	if got := SiteSlug("../CONTRIBUTING.md"); got != "CONTRIBUTING" {
		t.Errorf("root slug = %q", got)
	}
	if got := RawPageURL("../CONTRIBUTING.md"); got != GitHubRaw+"CONTRIBUTING.md" {
		t.Errorf("raw url = %q", got)
	}
	if got := SiteRedirectURL("features/hooks.md"); got != "https://coddy.dev/docs/features/hooks" {
		t.Errorf("redirect url = %q", got)
	}
	nav := &Nav{Groups: []Group{{ID: "g", Title: "G", Pages: []Page{{Path: "features/hooks.md", Title: "Hooks", Summary: "s"}, {Path: "../CONTRIBUTING.md", Title: "Contributing", Summary: "s"}}}}}
	files := RenderSite(nav)
	js, ok := files[SiteRedirectScript]
	if !ok || len(files) != 1 {
		t.Fatalf("site files: %v", files)
	}
	for _, want := range []string{
		`var ROOT = {"CONTRIBUTING":"CONTRIBUTING.md"};`,
		`var BLOB = "https://github.com/coddy-project/coddy-agent/blob/main/";`,
		`var RAW = "https://raw.githubusercontent.com/coddy-project/coddy-agent/main/";`,
		"window.coddyDocsTarget = coddyDocsTarget;",
		"window.location.replace(target)",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("missing %q in the interceptor:\n%s", want, js)
		}
	}
}

func TestRenderHubAndLLMSIndex(t *testing.T) {
	nav := &Nav{Groups: []Group{{ID: "g", Title: "Getting started", Summary: "Install.", Pages: []Page{{Path: "getting-started/install.md", Title: "Install", Summary: "How."}, {Path: "../CONTRIBUTING.md", Title: "Contributing", Summary: "Why."}}}}}
	hub := RenderHub(nav)
	if !strings.Contains(hub, "## Getting started\n\nInstall.\n\n- [Install](getting-started/install.md) - How.\n- [Contributing](../CONTRIBUTING.md) - Why.") {
		t.Fatalf("hub:\n%s", hub)
	}
	idx := RenderLLMSIndex(nav, "# Coddy documentation\n\nOne binary.\n\n<!-- docsgen:nav:start -->\n", "https://raw.example/main/")
	if !strings.Contains(idx, "> One binary.") || !strings.Contains(idx, "(https://raw.example/main/docs/getting-started/install.md): How.") || !strings.Contains(idx, "(https://raw.example/main/CONTRIBUTING.md): Why.") {
		t.Fatalf("llms index:\n%s", idx)
	}
}

// The llms files are built for the site on every run and are not kept in this
// repository: a copy in the index bought nothing and conflicted in every branch
// that touched any page, because both sides regenerate the same concatenation
// of all of them.
func TestLLMSFilesAreRenderedButNotKeptInTheRepository(t *testing.T) {
	res := &Result{Files: map[string]string{
		LLMSFile:         "index",
		LLMSFullFile:     "everything",
		"docs/README.md": "hub",
	}}
	root := t.TempDir()
	if err := res.Write(root); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{LLMSFile, LLMSFullFile} {
		if _, err := os.Stat(filepath.Join(root, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s was written into the repository", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "docs/README.md")); err != nil {
		t.Fatalf("a page that does belong here was not written: %v", err)
	}
	// And their absence is not a staleness problem, or a fresh checkout would
	// fail the check it is supposed to pass.
	for _, p := range res.Stale(root) {
		if p.File == LLMSFile || p.File == LLMSFullFile {
			t.Fatalf("a published-only file was reported as stale: %v", p)
		}
	}
}
