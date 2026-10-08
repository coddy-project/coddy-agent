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

func TestLangTakesEverySpellingOfALocale(t *testing.T) {
	for in, want := range map[string]string{
		"ru":          Russian,
		"RU":          Russian,
		"ru-RU":       Russian,
		"ru_RU.UTF-8": Russian,
		"ru_UA":       Russian,
		"en":          English,
		"en_US.UTF-8": English,
		"":            English,
		"C":           English,
		"POSIX":       English,
		"de-DE":       English, // no translation yet: English
		"uk":          English,
		" ru ":        Russian,
	} {
		if got := Lang(in); got != want {
			t.Errorf("Lang(%q) = %q, want %q", in, got, want)
		}
	}
	if !reflect.DeepEqual(Languages, []string{English, Russian}) {
		t.Fatalf("Languages = %v", Languages)
	}
}

// KnownLang names a documentation language only for a locale written in one.
func TestKnownLangNamesOnlyTheLanguagesOfTheDocumentation(t *testing.T) {
	for in, want := range map[string]string{"ru": Russian, "ru_RU.UTF-8": Russian, "en-GB": English, "EN": English, "uk": "", "de_DE": "", "": "", "C": ""} {
		got, ok := KnownLang(in)
		if got != want || ok != (want != "") {
			t.Errorf("KnownLang(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

// CODDY_LANG overrides the terminal's locale; the locale is LC_ALL, then
// LC_MESSAGES, then LANG: the first one set decides, as POSIX orders them.
func TestLangFromEnvFollowsThePOSIXOrder(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	for _, tc := range []struct {
		vars map[string]string
		want string
	}{
		{map[string]string{"LANG": "ru_RU.UTF-8"}, Russian},
		{map[string]string{"LANG": "en_US.UTF-8"}, English},
		{map[string]string{"LC_MESSAGES": "ru_RU.UTF-8", "LANG": "en_US.UTF-8"}, Russian},
		{map[string]string{"LC_ALL": "en_US.UTF-8", "LC_MESSAGES": "ru_RU.UTF-8", "LANG": "ru_RU.UTF-8"}, English},
		{map[string]string{"LC_ALL": "", "LANG": "ru_RU.UTF-8"}, Russian},
		{map[string]string{"CODDY_LANG": "ru", "LC_ALL": "en_US.UTF-8"}, Russian},
		{map[string]string{"CODDY_LANG": "en", "LANG": "ru_RU.UTF-8"}, English},
		{map[string]string{}, English},
	} {
		if got := LangFromEnv(env(tc.vars)); got != tc.want {
			t.Errorf("LangFromEnv(%v) = %q, want %q", tc.vars, got, tc.want)
		}
	}
}

// A text with Cyrillic letters is Russian; anything else is English.
func TestLangOfText(t *testing.T) {
	for in, want := range map[string]string{
		"telegram proxy":                  English,
		"прокси для telegram":             Russian,
		"agent.max_turns ходов":           Russian,
		"@coddy:features/mcp explain":     English,
		"@coddy:features/mcp объясни это": Russian,
		"":     English,
		"Ёлка": Russian,
	} {
		if got := LangOfText(in); got != want {
			t.Errorf("LangOfText(%q) = %q, want %q", in, got, want)
		}
	}
}

// translatedFS is the test documentation of testLibrary plus a Russian tree:
// guide/start and ref/keys are translated, guide/proxy is not.
func translatedFS() fstest.MapFS {
	fsys := testFS()
	fsys["ru/nav.yaml"] = &fstest.MapFile{Data: []byte(`groups:
  guide:
    title: Руководство
    summary: Руководство.
  ref:
    title: Справочник
    summary: Списки.
pages:
  guide/start.md:
    title: Как начать
    summary: Установите программу и запустите её.
  guide/proxy.md:
    title: Прокси
    summary: Запросы через прокси.
  ../CONTRIBUTING.md:
    title: Участие в разработке
    summary: Вне дерева.
  ref/keys.md:
    title: Ключи
    summary: Все ключи конфигурации.
`)}
	fsys["ru/guide/start.md"] = &fstest.MapFile{Data: []byte("# Как начать\n\nЗапустите `coddy` после [установки](../ref/keys.md#agentmax_turns).\n\n## Установка\n\nСкачайте архив. Смотрите [прокси](proxy.md) и [выше](#установка), а ещё [то же по-английски](#install).\n\n![снимок](../../assets/start.png)\n\nКод лежит в [proxy.go](../../../internal/llm/proxy.go).\n\n```bash\n# не заголовок\necho \"[x](proxy.md)\"\n```\n\n### Установка\n\nВторой заголовок установки.\n\n## Telegram-бот\n\nБот ходит в Telegram через прокси шлюза, а сессии ботов живут отдельно.\n\n<!-- docsgen:source sha256=0123456789abcdef -->\n")}
	fsys["ru/ref/keys.md"] = &fstest.MapFile{Data: []byte("# Ключи\n\n## agent.max_turns\n\nПредел ходов ReAct, коротко max_turns.\n\n## gateways.telegram.proxy\n\nМаршрут Telegram-бота.\n")}
	return fsys
}

func russianLibrary(t *testing.T) *Library {
	t.Helper()
	lib, err := LoadLang(translatedFS(), "dev", Russian)
	if err != nil {
		t.Fatal(err)
	}
	return lib
}

func TestLoadLangReadsTheTranslationWithTheMapOfTheSource(t *testing.T) {
	lib := russianLibrary(t)
	if lib.Lang != Russian {
		t.Fatalf("Lang = %q", lib.Lang)
	}
	var slugs []string
	for _, p := range lib.Pages() {
		slugs = append(slugs, p.Slug)
	}
	if want := []string{"guide/start", "guide/proxy", "ref/keys"}; !reflect.DeepEqual(slugs, want) {
		t.Fatalf("pages %v, want the order of the English map %v", slugs, want)
	}
	if lib.Groups[0].Title != "Руководство" || lib.Groups[1].Summary != "Списки." {
		t.Fatalf("groups %q / %q", lib.Groups[0].Title, lib.Groups[1].Summary)
	}
	start, _ := lib.Page("guide/start")
	if start.Title != "Как начать" || start.Summary != "Установите программу и запустите её." || start.Lang != Russian {
		t.Fatalf("start: %q %q %q", start.Title, start.Summary, start.Lang)
	}
	if !strings.HasPrefix(start.Markdown, "# Как начать") {
		t.Fatalf("start is not the Russian page:\n%s", start.Markdown)
	}
	if strings.Contains(start.Markdown, "docsgen:source") {
		t.Fatalf("the stamp reached the reader:\n%s", start.Markdown)
	}
	if got := start.SiteURL(); got != "https://coddy.dev/ru/docs/guide/start" {
		t.Fatalf("SiteURL = %q", got)
	}
	// A page with no translation is the English page under the Russian title.
	proxy, _ := lib.Page("guide/proxy")
	if proxy.Lang != English || proxy.Title != "Прокси" || !strings.HasPrefix(proxy.Markdown, "# Proxies") {
		t.Fatalf("fallback: %q %q\n%s", proxy.Lang, proxy.Title, proxy.Markdown)
	}
	if lib.Prev(start) != nil || lib.Next(start).Slug != "guide/proxy" {
		t.Fatal("prev/next do not follow the map")
	}
}

// A Russian heading answers to the anchor of the English heading at the same
// position, so one address names a section in both languages; Local is the
// anchor GitHub gives the Russian text.
func TestTranslatedHeadingsCarryTheAnchorsOfTheSource(t *testing.T) {
	lib := russianLibrary(t)
	start, _ := lib.Page("guide/start")
	var anchors, locals, texts []string
	for _, h := range start.Headings {
		anchors = append(anchors, h.Anchor)
		locals = append(locals, h.Local)
		texts = append(texts, h.Text)
	}
	if want := []string{"getting-going", "install", "install-1", "telegram-bot"}; !reflect.DeepEqual(anchors, want) {
		t.Fatalf("anchors %v, want %v", anchors, want)
	}
	if want := []string{"как-начать", "установка", "установка-1", "telegram-бот"}; !reflect.DeepEqual(locals, want) {
		t.Fatalf("local anchors %v, want %v", locals, want)
	}
	if want := []string{"Как начать", "Установка", "Установка", "Telegram-бот"}; !reflect.DeepEqual(texts, want) {
		t.Fatalf("texts %v, want %v", texts, want)
	}
	// In English the local anchor is the anchor.
	en := testLibrary(t, "dev")
	enStart, _ := en.Page("guide/start")
	for _, h := range enStart.Headings {
		if h.Local != h.Anchor {
			t.Fatalf("English heading %q: Local %q, Anchor %q", h.Text, h.Local, h.Anchor)
		}
	}
}

func TestTranslatedLinksUseTheAnchorsOfTheSource(t *testing.T) {
	lib, err := LoadLang(translatedFS(), "1.1.55", Russian)
	if err != nil {
		t.Fatal(err)
	}
	start, _ := lib.Page("guide/start")
	for _, want := range []string{
		"[установки](coddy:ref/keys#agentmax_turns)",
		"[прокси](coddy:guide/proxy)",
		"[выше](coddy:guide/start#install)",
		"[то же по-английски](coddy:guide/start#install)",
		"![снимок](https://raw.githubusercontent.com/coddy-project/coddy-agent/1.1.55/docs/assets/start.png)",
		"[proxy.go](https://github.com/coddy-project/coddy-agent/blob/1.1.55/internal/llm/proxy.go)",
		"echo \"[x](proxy.md)\"",
	} {
		if !strings.Contains(start.Markdown, want) {
			t.Errorf("Russian page lacks %q:\n%s", want, start.Markdown)
		}
	}
}

func TestResolveTakesBothAnchorsOfATranslatedSection(t *testing.T) {
	lib := russianLibrary(t)
	for _, tc := range []struct{ in, slug, anchor string }{
		{"guide/start#install", "guide/start", "install"},
		{"guide/start#установка", "guide/start", "install"},
		{"guide/start#Установка-1", "guide/start", "install-1"},
		{"guide/start#%D1%83%D1%81%D1%82%D0%B0%D0%BD%D0%BE%D0%B2%D0%BA%D0%B0", "guide/start", "install"},
		{"coddy:guide/start#telegram-бот", "guide/start", "telegram-bot"},
		{"ru/guide/start", "guide/start", ""},
		{"docs/ru/guide/start.md", "guide/start", ""},
		{"https://coddy.dev/ru/docs/guide/start#install", "guide/start", "install"},
		{"https://coddy.dev/docs/guide/start", "guide/start", ""},
		{"Как начать", "guide/start", ""},
		{"Getting going", "guide/start", ""},
		{"proxies", "guide/proxy", ""},
		{"как начать", "guide/start", ""},
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
	p, _ := lib.Page("guide/start")
	h, md, ok := p.Section("установка")
	if !ok || h.Anchor != "install" || !strings.HasPrefix(md, "## Установка") {
		t.Fatalf("Section by the Russian anchor: %+v %q %v", h, md, ok)
	}
	if r, err := p.Read(ReadOptions{Anchor: "install"}); err != nil || !strings.HasPrefix(r.Text, "## Установка") {
		t.Fatalf("Read by the canonical anchor: %+v %v", r, err)
	}
}

func TestSearchFindsRussianWordsInTheirForms(t *testing.T) {
	lib := russianLibrary(t)
	for _, tc := range []struct{ query, top string }{
		{"скачать архив", "guide/start#install"},
		{"архивы", "guide/start#install"},
		{"установку", "guide/start#install-1"},
		{"бот", "guide/start#telegram-bot"},
		{"ботов", "guide/start#telegram-bot"},
		{"сессия бота", "guide/start#telegram-bot"},
		{"max_turns", "ref/keys#agentmax_turns"},
		{"ходы", "ref/keys#agentmax_turns"},
		{"как начать", "guide/start"},
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
	// A word typed letter by letter finds the page before it is finished.
	if hits := lib.Search("устан", 3); len(hits) == 0 || hits[0].Slug != "guide/start" {
		t.Errorf("Search(устан) = %+v", hits)
	}
	if hits := lib.Search("и в на", 5); len(hits) != 0 {
		t.Errorf("Russian stop words alone found %d sections", len(hits))
	}
	found := false
	for _, h := range lib.Search("telegram", 5) {
		if h.Slug == "guide/start" {
			found = true
			if h.Title != "Как начать" || h.Group != "Руководство" {
				t.Fatalf("hit does not carry the Russian titles: %+v", h)
			}
		}
	}
	if !found {
		t.Fatal("telegram does not find guide/start")
	}
}

// The Snowball Russian stemmer: the forms of a word share a stem.
func TestStemRussian(t *testing.T) {
	for in, want := range map[string]string{
		"вагон":        "вагон",
		"вагона":       "вагон",
		"вагоне":       "вагон",
		"вагонов":      "вагон",
		"вагоном":      "вагон",
		"вагоны":       "вагон",
		"важная":       "важн",
		"важнейшие":    "важн",
		"важную":       "важн",
		"сессия":       "сесс",
		"сессии":       "сесс",
		"сессий":       "сесс",
		"сессиями":     "сесс",
		"настройка":    "настройк",
		"настройки":    "настройк",
		"настройках":   "настройк",
		"документация": "документац",
		"документации": "документац",
		"запускается":  "запуска",
		"запускает":    "запуска",
		"поиск":        "поиск",
		"поиска":       "поиск",
		"ход":          "ход",
	} {
		if got := stemRussian(in); got != want {
			t.Errorf("stemRussian(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTokenizeRussian(t *testing.T) {
	got := tokenize("Сессии и настройки Ещё ёлки, agent.max_turns в Telegram")
	want := []string{"сесс", "настройк", "елк", "agent", "max_turns", "max", "turn", "telegram"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize = %v, want %v", got, want)
	}
}

// Budgets count characters, not bytes, so a Russian page is not cut at half
// the length of an English one.
func TestReadAndSnippetsCountCharacters(t *testing.T) {
	fsys := fstest.MapFS{
		"nav.yaml":  {Data: []byte("groups:\n  - id: g\n    title: G\n    summary: s\n    pages:\n      - path: g/p.md\n        title: P\n        summary: s\n")},
		"g/p.md":    {Data: []byte("# P\n\n" + strings.Repeat("слово ", 9) + "\n\n" + strings.Repeat("слово ", 9) + "\n")},
		"ru/g/p.md": {Data: []byte("# П\n\n" + strings.Repeat("слово ", 9) + "\n\n" + strings.Repeat("слово ", 9) + "\n")},
	}
	lib, err := Load(fsys, "dev")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := lib.Page("g/p")
	// Each line of words is 54 characters (108 bytes) plus its newline: a
	// budget of 70 characters holds lines 1-4, where 70 bytes would stop
	// at line 2.
	r, err := p.Read(ReadOptions{MaxChars: 70})
	if err != nil {
		t.Fatal(err)
	}
	if r.To != 4 {
		t.Fatalf("read lines 1-%d under 70 characters, want 1-4", r.To)
	}
	frags := snippet(strings.Repeat("документации ", 60), map[string]bool{"документац": true})
	var n int
	for _, f := range frags {
		n += len([]rune(f.Text))
	}
	if n < snippetChars-10 {
		t.Fatalf("a Russian snippet holds %d characters, want about %d", n, snippetChars)
	}
}

// The binary carries a Russian page for every page of the map under docs/,
// with the Russian title and summary of docs/ru/nav.yaml.
func TestRussianLibraryCarriesEveryPageOfTheMap(t *testing.T) {
	lib, err := For(Russian)
	if err != nil {
		t.Fatalf("the embedded Russian documentation does not load: %v", err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "ru", "nav.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var nav translatedNav
	if err := yaml.Unmarshal(data, &nav); err != nil {
		t.Fatal(err)
	}
	en, err := For(English)
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Pages()) != len(en.Pages()) {
		t.Fatalf("Russian library has %d pages, English %d", len(lib.Pages()), len(en.Pages()))
	}
	for _, p := range lib.Pages() {
		want := nav.Pages[p.Slug+".md"]
		if p.Lang != Russian {
			t.Errorf("page %s has no Russian text (add docs/ru/%s.md)", p.Slug, p.Slug)
		}
		if p.Title != want.Title || p.Summary != want.Summary {
			t.Errorf("page %s: got %q / %q, docs/ru/nav.yaml says %q / %q", p.Slug, p.Title, p.Summary, want.Title, want.Summary)
		}
		if !strings.HasPrefix(p.Markdown, "# ") {
			t.Errorf("page %s does not start with a title", p.Slug)
		}
	}
	if d, _ := Default(); d != en {
		t.Fatal("Default is not the English library")
	}
	if other, _ := For("de"); other != en {
		t.Fatal("a language with no translation is not English")
	}
}
