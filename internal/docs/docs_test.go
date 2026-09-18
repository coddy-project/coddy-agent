package docs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"
)

func defaultLibrary(t *testing.T) *Library {
	t.Helper()
	lib, err := Default()
	if err != nil {
		t.Fatalf("the embedded documentation does not load: %v", err)
	}
	return lib
}

// The binary carries every page the map lists under docs/, with the map's
// title and summary: a new documentation group the embed pattern forgot
// fails here, not in a reader.
func TestDefaultCarriesEveryPageOfTheMap(t *testing.T) {
	lib := defaultLibrary(t)
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "nav.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var nav navFile
	if err := yaml.Unmarshal(data, &nav); err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, g := range nav.Groups {
		for _, p := range g.Pages {
			if strings.HasPrefix(p.Path, "../") {
				continue
			}
			want++
			slug := strings.TrimSuffix(p.Path, ".md")
			page, ok := lib.Page(slug)
			if !ok {
				t.Errorf("page %s of nav.yaml is not embedded (add its folder to docs/embed.go)", p.Path)
				continue
			}
			if page.Title != p.Title || page.Summary != p.Summary || page.Group.ID != g.ID {
				t.Errorf("page %s: got %q / %q in %s", slug, page.Title, page.Summary, page.Group.ID)
			}
			if !strings.HasPrefix(page.Markdown, "# ") {
				t.Errorf("page %s does not start with its title", slug)
			}
		}
	}
	if len(lib.Pages()) != want {
		t.Errorf("embedded %d pages, the map lists %d under docs/", len(lib.Pages()), want)
	}
}

// Every page of the real documentation is found by its own title, near the
// top: that is how a reader looks for a chapter.
func TestSearchFindsEveryPageByItsTitle(t *testing.T) {
	lib := defaultLibrary(t)
	for _, p := range lib.Pages() {
		hits := lib.Search(p.Title, 5)
		found := false
		for _, h := range hits {
			if h.Slug == p.Slug {
				found = true
				break
			}
		}
		if !found {
			var got []string
			for _, h := range hits {
				got = append(got, h.Ref())
			}
			t.Errorf("searching %q does not find %s in the top five: %v", p.Title, p.Slug, got)
		}
	}
}

func testLibrary(t *testing.T, ver string) *Library {
	t.Helper()
	fsys := fstest.MapFS{
		"nav.yaml": {Data: []byte(`groups:
  - id: guide
    title: Guide
    summary: The guide.
    pages:
      - path: guide/start.md
        title: Getting going
        summary: Install the tool and run it.
      - path: guide/proxy.md
        title: Proxies
        summary: Routing requests through a proxy.
      - path: ../CONTRIBUTING.md
        title: Contributing
        summary: Outside the tree.
  - id: ref
    title: Reference
    summary: Lists.
    pages:
      - path: ref/keys.md
        title: Keys
        summary: Every configuration key.
`)},
		"guide/start.md": {Data: []byte("# Getting going\n\nRun `coddy` after the [install](../ref/keys.md#agentmax_turns).\n\n## Install\n\nDownload the archive. See [proxies](proxy.md) and [above](#install).\n\n![shot](../assets/start.png)\n\n```bash\n# not a heading\necho \"[x](proxy.md)\"\n```\n\n### Install\n\nThe second install heading.\n\n## Telegram bot\n\nThe bot talks to Telegram through the proxy of the gateway.\n")},
		"guide/proxy.md": {Data: []byte("# Proxies\n\nEvery provider has a proxy setting: inherit, none or a URL.\n\n## Provider proxy\n\nThe proxy of a provider row routes its completions.\n\n## Environment\n\nHTTPS_PROXY is read when the setting is inherit. Code lives in [proxy.go](../../internal/llm/proxy.go).\n")},
		"ref/keys.md":    {Data: []byte("# Keys\n\n## agent.max_turns\n\nThe cap on ReAct rounds, max_turns for short.\n\n## gateways.telegram.proxy\n\nThe route of the Telegram bot.\n")},
	}
	lib, err := Load(fsys, ver)
	if err != nil {
		t.Fatal(err)
	}
	return lib
}

func TestLoadSkipsPagesOutsideTheTreeAndKeepsMapOrder(t *testing.T) {
	lib := testLibrary(t, "dev")
	var slugs []string
	for _, p := range lib.Pages() {
		slugs = append(slugs, p.Slug)
	}
	if want := []string{"guide/start", "guide/proxy", "ref/keys"}; !reflect.DeepEqual(slugs, want) {
		t.Fatalf("pages %v, want %v", slugs, want)
	}
	start, _ := lib.Page("guide/start")
	if lib.Prev(start) != nil || lib.Next(start).Slug != "guide/proxy" || lib.Next(lib.Pages()[2]) != nil {
		t.Fatal("prev/next do not follow the map")
	}
}

func TestLoadFailsOnAPageTheTreeLacks(t *testing.T) {
	fsys := fstest.MapFS{"nav.yaml": {Data: []byte("groups:\n  - id: g\n    title: G\n    summary: s\n    pages:\n      - path: g/missing.md\n        title: M\n        summary: s\n")}}
	if _, err := Load(fsys, "dev"); err == nil || !strings.Contains(err.Error(), "g/missing.md") {
		t.Fatalf("want an error naming the missing page, got %v", err)
	}
}

func TestHeadingsSkipCodeAndNumberRepeats(t *testing.T) {
	lib := testLibrary(t, "dev")
	start, _ := lib.Page("guide/start")
	var got []string
	for _, h := range start.Headings {
		got = append(got, h.Anchor)
	}
	if want := []string{"getting-going", "install", "install-1", "telegram-bot"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("anchors %v, want %v", got, want)
	}
	if got := Anchor("Composer **`@`** mentions (beta)"); got != "composer--mentions-beta" {
		t.Fatalf("Anchor = %q", got)
	}
}

func TestLinksAreRewrittenForAReaderOutsideTheRepository(t *testing.T) {
	for _, tc := range []struct {
		ver, ref string
	}{{"dev", "main"}, {"1.1.55", "1.1.55"}, {"v2.0.1", "2.0.1"}, {"1.1.55-3-gabc", "main"}} {
		lib := testLibrary(t, tc.ver)
		start, _ := lib.Page("guide/start")
		md := start.Markdown
		for _, want := range []string{
			"[install](coddy:ref/keys#agentmax_turns)",
			"[proxies](coddy:guide/proxy)",
			"[above](coddy:guide/start#install)",
			"![shot](https://raw.githubusercontent.com/coddy-project/coddy-agent/" + tc.ref + "/docs/assets/start.png)",
			"Run `coddy` after",
			"echo \"[x](proxy.md)\"",
		} {
			if !strings.Contains(md, want) {
				t.Errorf("version %s: page lacks %q:\n%s", tc.ver, want, md)
			}
		}
		proxy, _ := lib.Page("guide/proxy")
		if want := "[proxy.go](https://github.com/coddy-project/coddy-agent/blob/" + tc.ref + "/internal/llm/proxy.go)"; !strings.Contains(proxy.Markdown, want) {
			t.Errorf("version %s: repository link not rewritten: %s", tc.ver, proxy.Markdown)
		}
	}
}

func TestResolveTakesEverySpellingOfAPage(t *testing.T) {
	lib := testLibrary(t, "dev")
	for _, tc := range []struct{ in, slug, anchor string }{
		{"guide/proxy", "guide/proxy", ""},
		{"guide/proxy.md", "guide/proxy", ""},
		{"docs/guide/proxy.md", "guide/proxy", ""},
		{"coddy:guide/proxy#environment", "guide/proxy", "environment"},
		{"@coddy:guide/proxy#Environment", "guide/proxy", "environment"},
		{"https://coddy.dev/docs/guide/proxy", "guide/proxy", ""},
		{"coddy.dev/docs/ref/keys#agentmax_turns", "ref/keys", "agentmax_turns"},
		{" proxy ", "guide/proxy", ""},
		{"Getting going", "guide/start", ""},
		{"GUIDE/PROXY", "guide/proxy", ""},
	} {
		p, anchor, err := lib.Resolve(tc.in)
		if err != nil {
			t.Errorf("Resolve(%q): %v", tc.in, err)
			continue
		}
		if p.Slug != tc.slug || anchor != tc.anchor {
			t.Errorf("Resolve(%q) = %s#%s, want %s#%s", tc.in, p.Slug, anchor, tc.slug, tc.anchor)
		}
	}
}

func TestResolveExplainsWhatItCannotFind(t *testing.T) {
	lib := testLibrary(t, "dev")
	if _, _, err := lib.Resolve("guide/proxies-and-telegram"); err == nil || !strings.Contains(err.Error(), "closest:") || !strings.Contains(err.Error(), "guide/proxy") {
		t.Errorf("unknown page: %v", err)
	}
	if _, _, err := lib.Resolve("guide/proxy#nowhere"); err == nil || !strings.Contains(err.Error(), "provider-proxy") {
		t.Errorf("unknown section should list the sections: %v", err)
	}
	if _, _, err := lib.Resolve("  "); err == nil {
		t.Error("an empty reference resolved")
	}
}

func TestSearchRanksTheSectionThatAnswers(t *testing.T) {
	lib := testLibrary(t, "dev")
	for _, tc := range []struct{ query, top string }{
		{"telegram proxy", "ref/keys#gatewaystelegramproxy"},
		{"max_turns", "ref/keys#agentmax_turns"},
		{"turns", "ref/keys#agentmax_turns"},
		{"HTTPS_PROXY inherit", "guide/proxy#environment"},
		{"Getting going", "guide/start"},
		{"prox", "guide/proxy"},
	} {
		hits := lib.Search(tc.query, 3)
		if len(hits) == 0 || hits[0].Ref() != tc.top {
			var got []string
			for _, h := range hits {
				got = append(got, h.Ref())
			}
			t.Errorf("Search(%q) = %v, want %s first", tc.query, got, tc.top)
		}
	}
	if hits := lib.Search("the and of", 5); len(hits) != 0 {
		t.Errorf("stopwords alone found %d sections", len(hits))
	}
	if hits := lib.Search("zzzqqq", 5); len(hits) != 0 {
		t.Errorf("nonsense found %d sections", len(hits))
	}
}

func TestSearchCapsSectionsPerPage(t *testing.T) {
	lib := testLibrary(t, "dev")
	count := map[string]int{}
	for _, h := range lib.Search("proxy", 20) {
		count[h.Slug]++
	}
	for slug, n := range count {
		if n > perPage {
			t.Errorf("%s returned %d sections, cap %d", slug, n, perPage)
		}
	}
}

func TestSnippetMarksTheMatchedWords(t *testing.T) {
	lib := testLibrary(t, "dev")
	hits := lib.Search("telegram bot", 1)
	if len(hits) == 0 {
		t.Fatal("no hit")
	}
	var marked []string
	for _, f := range hits[0].Snippet {
		if f.Hit {
			marked = append(marked, strings.ToLower(f.Text))
		}
	}
	if len(marked) == 0 {
		t.Fatalf("no word marked in %+v", hits[0].Snippet)
	}
	for _, w := range marked {
		if !strings.Contains(w, "telegram") && !strings.Contains(w, "bot") {
			t.Errorf("marked %q", w)
		}
	}
}

func TestTokenize(t *testing.T) {
	got := tokenize("The agent.max_turns key, Sessions and Proxies; Сессии")
	want := []string{"agent", "max_turns", "max", "turn", "key", "session", "proxy", "сессии"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize = %v, want %v", got, want)
	}
}

func TestReadSectionsAndContinuations(t *testing.T) {
	lib := testLibrary(t, "dev")
	p, _ := lib.Page("guide/start")
	r, err := p.Read(ReadOptions{Anchor: "install"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Heading == nil || r.Heading.Text != "Install" || !strings.HasPrefix(r.Text, "## Install") || !strings.Contains(r.Text, "The second install heading.") || strings.Contains(r.Text, "Telegram") {
		t.Fatalf("section reading: %+v", r)
	}
	r, err = p.Read(ReadOptions{MaxBytes: 40})
	if err != nil {
		t.Fatal(err)
	}
	if r.From != 1 || r.Next == 0 || r.To >= r.Total {
		t.Fatalf("a bounded reading does not continue: %+v", r)
	}
	next, err := p.Read(ReadOptions{Offset: r.Next, MaxBytes: -1})
	if err != nil || next.To != next.Total || next.Next != 0 || !strings.HasSuffix(next.Text, "the proxy of the gateway.") {
		t.Fatalf("continuation: %+v %v", next, err)
	}
	if _, err := p.Read(ReadOptions{Anchor: "nowhere"}); err == nil {
		t.Fatal("an unknown section read")
	}
	if _, err := p.Read(ReadOptions{Offset: 999}); err == nil {
		t.Fatal("an offset past the end read")
	}
	if !strings.Contains(p.Outline(), "  - #install-1  Install") {
		t.Fatalf("outline:\n%s", p.Outline())
	}
	if !strings.Contains(lib.Contents(), "- guide/proxy - Proxies: Routing requests through a proxy.") {
		t.Fatalf("contents:\n%s", lib.Contents())
	}
}

func TestSnippetMarksTheWordWithoutItsPunctuation(t *testing.T) {
	frags := snippet("Set the (proxy). Then restart.", map[string]bool{"proxy": true})
	var marked []string
	for _, f := range frags {
		if f.Hit {
			marked = append(marked, f.Text)
		}
	}
	if !reflect.DeepEqual(marked, []string{"proxy"}) {
		t.Fatalf("marked %q in %+v", marked, frags)
	}
	var whole strings.Builder
	for _, f := range frags {
		whole.WriteString(f.Text)
	}
	if whole.String() != "Set the (proxy). Then restart." {
		t.Fatalf("the snippet reads %q", whole.String())
	}
}
