package docsgen

import (
	"os"
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

func TestCheckNavListsEveryPageOnce(t *testing.T) {
	root := t.TempDir()
	write(t, root, NavFile, "groups:\n  - id: g\n    title: G\n    pages:\n      - path: one.md\n        title: One\n        summary: s\n      - path: missing.md\n        title: Missing\n        summary: s\n")
	write(t, root, "docs/one.md", "# One\n")
	write(t, root, "docs/orphan.md", "# Orphan\n")
	write(t, root, "docs/old.md", "<!-- docs-stub: moved to docs/one.md -->\n# Moved\n")
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
	for _, p := range problems {
		if p.File == "docs/old.md" {
			t.Errorf("stub reported: %v", p)
		}
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

func TestSiteSlugsAndTwinLinks(t *testing.T) {
	if got := SiteSlug("getting-started/install.md"); got != "getting-started/install" {
		t.Errorf("slug = %q", got)
	}
	if got := SiteSlug("../CONTRIBUTING.md"); got != "CONTRIBUTING" {
		t.Errorf("root slug = %q", got)
	}
	if got := SitePageURL("features/hooks.md"); got != "https://coddy.dev/docs/features/hooks.md" {
		t.Errorf("twin url = %q", got)
	}
	twin := twinContent("docs/features/hooks.md", "![a](../assets/x.png) [b](../operate/serve.md#anchor) [c](../../DESIGN.md) [d](https://example.com) [e](../plans/hooks.md)")
	for _, want := range []string{
		"![a](https://raw.githubusercontent.com/coddy-project/coddy-agent/main/docs/assets/x.png)",
		"[b](../operate/serve.md#anchor)",
		"[c](https://github.com/coddy-project/coddy-agent/blob/main/DESIGN.md)",
		"[d](https://example.com)",
		"[e](../plans/hooks.md)",
	} {
		if !strings.Contains(twin, want) {
			t.Errorf("missing %q in %q", want, twin)
		}
	}
	page := redirectPage("Hooks", "https://github.com/coddy-project/coddy-agent/blob/main/docs/features/hooks.md", "hooks.md")
	if !strings.Contains(page, `http-equiv="refresh"`) || !strings.Contains(page, "location.hash") || !strings.Contains(page, `name="robots" content="noindex"`) {
		t.Errorf("redirect page:\n%s", page)
	}
}

func TestRenderHubAndLLMSIndex(t *testing.T) {
	nav := &Nav{Groups: []Group{{ID: "g", Title: "Getting started", Summary: "Install.", Pages: []Page{{Path: "getting-started/install.md", Title: "Install", Summary: "How."}, {Path: "../CONTRIBUTING.md", Title: "Contributing", Summary: "Why."}}}}}
	hub := RenderHub(nav)
	if !strings.Contains(hub, "## Getting started\n\nInstall.\n\n- [Install](getting-started/install.md) - How.\n- [Contributing](../CONTRIBUTING.md) - Why.") {
		t.Fatalf("hub:\n%s", hub)
	}
	idx := RenderLLMSIndex(nav, "# Coddy documentation\n\nOne binary.\n\n<!-- docsgen:nav:start -->\n", "https://site.example/docs/")
	if !strings.Contains(idx, "> One binary.") || !strings.Contains(idx, "(https://site.example/docs/getting-started/install.md): How.") || !strings.Contains(idx, "(https://site.example/docs/CONTRIBUTING.md): Why.") {
		t.Fatalf("llms index:\n%s", idx)
	}
}
