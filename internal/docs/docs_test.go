package docs

import (
	"encoding/json"
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
	lib, err := Load(testFS(), ver)
	if err != nil {
		t.Fatal(err)
	}
	return lib
}

// testFS is a small documentation tree: two groups, a page outside the tree,
// links of every kind, a repeated heading and a heading inside code.
func testFS() fstest.MapFS {
	return fstest.MapFS{
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
		"guide/proxy.md": {Data: []byte("# Proxies\n\nEvery provider has a proxy setting: inherit, none or a URL.\n\n## Provider proxy\n\nThe proxy of a provider row routes its completions.\n\n## Environment\n\nHTTPS_PROXY is read when the setting is inherit. Code lives in [proxy.go](../../internal/llm/proxy.go) and [`proxy_test.go`](../../internal/llm/proxy_test.go), not in `[x](y.md)`.\n")},
		"ref/keys.md":    {Data: []byte("# Keys\n\n## agent.max_turns\n\nThe cap on ReAct rounds, max_turns for short.\n\n## gateways.telegram.proxy\n\nThe route of the Telegram bot.\n")},
	}
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
		for _, want := range []string{
			"[proxy.go](https://github.com/coddy-project/coddy-agent/blob/" + tc.ref + "/internal/llm/proxy.go)",
			// A link whose text is code is a link all the same.
			"[`proxy_test.go`](https://github.com/coddy-project/coddy-agent/blob/" + tc.ref + "/internal/llm/proxy_test.go)",
			"not in `[x](y.md)`.",
		} {
			if !strings.Contains(proxy.Markdown, want) {
				t.Errorf("version %s: repository link not rewritten (%s): %s", tc.ver, want, proxy.Markdown)
			}
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
	want := []string{"agent", "max_turns", "max", "turn", "key", "session", "proxy", "сесс"}
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
	r, err = p.Read(ReadOptions{MaxChars: 40})
	if err != nil {
		t.Fatal(err)
	}
	if r.From != 1 || r.Next == 0 || r.To >= r.Total {
		t.Fatalf("a bounded reading does not continue: %+v", r)
	}
	next, err := p.Read(ReadOptions{Offset: r.Next, MaxChars: -1})
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

// A section with no text of its own (a heading whose content is all in its
// subsections) still has a snippet: an empty list, never null in JSON.
func TestSnippetOfAnEmptySectionIsAnEmptyList(t *testing.T) {
	frags := snippet("", map[string]bool{"proxy": true})
	if frags == nil {
		t.Fatal("snippet of an empty text is nil")
	}
	data, err := json.Marshal(Hit{Snippet: frags})
	if err != nil || !strings.Contains(string(data), `"Snippet":[]`) {
		t.Fatalf("JSON %s %v", data, err)
	}
}

// A video is a GitHub attachment on a line of its own with the repository
// copy linked under it. The attachment plays only inside GitHub, so outside
// it the line becomes an embedded video of the repository copy, pinned to the
// release like an image; nothing of it is in the binary.
func TestVideoLinesPlayTheRepositoryCopy(t *testing.T) {
	fsys := fstest.MapFS{
		"nav.yaml": {Data: []byte("groups:\n  - id: g\n    title: G\n    summary: s\n    pages:\n      - path: g/p.md\n        title: P\n        summary: s\n")},
		"g/p.md":   {Data: []byte("# P\n\nhttps://github.com/user-attachments/assets/fd4837ae-23d0-400e-8e60-52706755bb4b\n\n*A recording. The file is in the repository as [swarm.mp4](../assets/video/swarm.mp4).*\n\nhttps://github.com/user-attachments/assets/00000000-0000-0000-0000-000000000000\n\nNo copy is linked here.\n")},
	}
	lib, err := Load(fsys, "1.1.55")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := lib.Page("g/p")
	if !strings.Contains(p.Markdown, "![Video: swarm.mp4](https://raw.githubusercontent.com/coddy-project/coddy-agent/1.1.55/docs/assets/video/swarm.mp4)") {
		t.Fatalf("the attachment line is not the repository copy:\n%s", p.Markdown)
	}
	if !strings.Contains(p.Markdown, "[swarm.mp4](https://github.com/coddy-project/coddy-agent/blob/1.1.55/docs/assets/video/swarm.mp4)") {
		t.Fatalf("the caption link changed:\n%s", p.Markdown)
	}
	if !strings.Contains(p.Markdown, "\nhttps://github.com/user-attachments/assets/00000000-0000-0000-0000-000000000000\n") {
		t.Fatalf("an attachment without a copy is left as it is:\n%s", p.Markdown)
	}
}

// rankFS is a documentation tree big enough for a ranking to mean something:
// English and Russian pages that share words (configuration, trust,
// servers, install) in titles, headings and bodies, so the weights of the
// fields, the length normalisation, the prefix matches and the coverage of a
// multi-word query all move the order.
func rankFS() fstest.MapFS {
	return fstest.MapFS{
		"nav.yaml": {Data: []byte(`groups:
  - id: guide
    title: Guide
    summary: Getting started.
    pages:
      - path: guide/install.md
        title: Installation
        summary: Install the binary, verify it and keep it current.
      - path: guide/proxy.md
        title: Proxies
        summary: Route provider requests through a proxy.
  - id: features
    title: Features
    summary: What Coddy does.
    pages:
      - path: features/memory.md
        title: Long-term memory
        summary: Notes the assistant keeps between sessions.
      - path: features/mcp.md
        title: MCP servers
        summary: Tools from servers the operator configures.
      - path: features/hooks.md
        title: Hooks
        summary: Commands that run at points of a session.
      - path: features/skills.md
        title: Скилы
        summary: Наборы инструкций, которые агент загружает по запросу.
  - id: operate
    title: Operate
    summary: Running Coddy.
    pages:
      - path: operate/serve.md
        title: Serving
        summary: Run coddy serve as a service.
      - path: operate/swarm.md
        title: Рой узлов
        summary: Релей и узлы, которые подключаются к нему.
  - id: reference
    title: Reference
    summary: Lists.
    pages:
      - path: reference/config.md
        title: Configuration reference
        summary: Every key of the configuration file.
`)},
		"guide/install.md":    {Data: []byte("# Installation\n\nDownload the archive for your platform and put the binary on the PATH.\n\n## Download\n\nThe release archives sit on the releases page, one per platform. The install script picks the right one.\n\n## Verify the checksum\n\nEvery archive has a checksum file. Compare it before you install the binary.\n\n## Upgrade packages\n\nA package manager owns the binary it installed. Upgrade it with the package manager, not with an update command.\n")},
		"guide/proxy.md":      {Data: []byte("# Proxies\n\nEvery provider has a proxy setting: inherit, none or a URL.\n\n## Provider proxy\n\nThe proxy of a provider row routes its completions, model lists and sign-in.\n\n## Environment\n\nHTTPS_PROXY is read when the setting is inherit. Servers that sit behind a corporate proxy need it.\n")},
		"features/memory.md":  {Data: []byte("# Long-term memory\n\nThe assistant keeps notes between sessions. A note is a markdown file under a global or a project folder.\n\n## Notes\n\nA note holds one fact. The memory subagent writes a note when you ask it to remember something and reads the notes before each turn.\n\n## Search\n\nThe subagent searches the notes by words. A query that names the folder or the file finds the note faster.\n\n## Scopes\n\nGlobal notes follow you into every project. Project notes stay with the checkout and are never shared with another project.\n")},
		"features/mcp.md":     {Data: []byte("# MCP servers\n\nAn MCP server gives the assistant tools. The operator declares servers in a file; the configuration of one server names its command or its address.\n\n## Configuration\n\nA server's command and arguments run exactly as written. The configuration file is read at the start and watched afterwards.\n\n## Trust gate\n\nA project file arrives with a checkout, so its servers are not started until the operator approves that exact declaration. Trust is bound to the workspace and to the digest of the declaration.\n\n## Shared servers\n\nSessions do not own a server. One connection is shared between sessions, and a server nothing holds is stopped after a delay.\n")},
		"features/hooks.md":   {Data: []byte("# Hooks\n\nA hook is a command that reads one JSON document on stdin at a point of a session and answers with an exit code.\n\n## Events\n\nHooks run before a tool call, after it, when a prompt is submitted and when the assistant stops. A hook can veto a tool call.\n\n## Trust\n\nA project hook file is held until the operator approves it. The trust receipt is bound to the file digest, so an edited hook asks again.\n")},
		"features/skills.md":  {Data: []byte("# Скилы\n\nСкил - это набор инструкций в файле SKILL.md. Агент загружает скил, когда задача к нему подходит.\n\n## Установка скилов\n\nСкилы ставятся из репозитория или из маркетплейса. Установка копирует файлы в папку скилов.\n\n## Обновление\n\nОбновление проверяет версии установленных скилов и ставит более новые. Скил, которого нет на диске, обновление не ставит.\n")},
		"operate/serve.md":    {Data: []byte("# Serving\n\nCoddy serve is the one long-running command. It starts every surface the configuration enables.\n\n## Systemd unit\n\nOn Linux the install command writes a systemd user unit and enables it for one account.\n\n## Restart policy\n\nThe dispatcher restarts a worker that exits with the restart status. A crash loop stops after a few tries and the unit reports it.\n\n## Configuration\n\nA change of the listen address needs a fresh process. Other changes of the configuration apply in place.\n")},
		"operate/swarm.md":    {Data: []byte("# Рой узлов\n\nРелей соединяет узлы роя. Узел подключается к релею и получает от него сессии.\n\n## Релей\n\nРелей не хранит сессий. Он ведёт реестр узлов и показывает их все в одном списке.\n\n## Настройка узла\n\nУзел подключается по адресу релея. Настройка занимает три строки в файле конфигурации.\n")},
		"reference/config.md": {Data: []byte("# Configuration reference\n\nEvery key of the configuration file, with its type and its default.\n\n## agent.max_turns\n\nThe cap on ReAct rounds of one turn, max_turns for short.\n\n## memory.max_search_hits\n\nThe number of notes a memory search returns.\n\n## mcp.project_trust\n\nWhether a project file of MCP servers is trusted: ask, allow or deny.\n\n## serve.listen\n\nThe address the server listens on. A change needs a restart.\n")},
	}
}

// The ranking of the documentation search is held to these orders: the core
// that ranks sections (tokenizing, stemming, the prefix matches, BM25F over
// the fields, the coverage of a multi-word query) is shared with other
// searches, and none of them may move a page of this one.
func TestSearchRankingIsPinned(t *testing.T) {
	lib, err := Load(rankFS(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"install", []string{"guide/install", "guide/install#verify-the-checksum", "guide/install#download", "operate/serve#systemd-unit"}},
		{"configuration", []string{"reference/config", "operate/serve#configuration", "features/mcp#configuration", "reference/config#agentmax_turns", "reference/config#memorymax_search_hits", "operate/serve"}},
		{"config", []string{"reference/config", "operate/serve#configuration", "features/mcp#configuration", "reference/config#agentmax_turns", "reference/config#memorymax_search_hits", "features/mcp"}},
		{"trust gate", []string{"features/mcp#trust-gate", "features/hooks#trust", "reference/config#mcpproject_trust"}},
		{"servers shared", []string{"features/mcp#shared-servers", "features/mcp", "features/memory#scopes", "features/mcp#configuration", "reference/config#servelisten", "reference/config#mcpproject_trust"}},
		{"restart policy systemd", []string{"operate/serve#restart-policy", "operate/serve#systemd-unit", "reference/config#servelisten"}},
		{"how do I restart the unit", []string{"operate/serve#restart-policy", "operate/serve#systemd-unit", "reference/config#servelisten"}},
		{"hooks events", []string{"features/hooks#events", "features/hooks", "features/hooks#trust"}},
		{"memory search", []string{"reference/config#memorymax_search_hits", "features/memory#search", "features/memory", "features/memory#notes"}},
		{"notes scopes", []string{"features/memory#scopes", "features/memory#notes", "features/memory", "reference/config#memorymax_search_hits"}},
		{"proxies", []string{"guide/proxy", "guide/proxy#provider-proxy", "guide/proxy#environment"}},
		{"max_turns", []string{"reference/config#agentmax_turns", "reference/config#memorymax_search_hits", "features/memory#notes"}},
		{"turns", []string{"reference/config#agentmax_turns", "features/memory#notes"}},
		{"скилы", []string{"features/skills", "features/skills#установка-скилов", "features/skills#обновление"}},
		{"установка скилов", []string{"features/skills#установка-скилов", "features/skills", "features/skills#обновление"}},
		{"обновление", []string{"features/skills#обновление"}},
		{"релей настройка", []string{"operate/swarm#настройка-узла", "operate/swarm#релей", "operate/swarm"}},
		{"реле", []string{"operate/swarm#релей", "operate/swarm", "operate/swarm#настройка-узла"}},
		{"узел", []string{"operate/swarm#настройка-узла", "operate/swarm"}},
		{"project trust digest", []string{"features/hooks#trust", "features/mcp#trust-gate", "reference/config#mcpproject_trust", "features/memory#scopes", "features/memory"}},
	} {
		var got []string
		for _, h := range lib.Search(tc.query, 6) {
			got = append(got, h.Ref())
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Search(%q) = %q, want %q", tc.query, got, tc.want)
		}
	}
}
