package docsgen

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// translatedTree writes a small documentation tree with its Russian twin:
// a map of one group, a page outside docs/, the hub and its translation, and
// the Russian map. Every Russian file is current unless a test changes it.
func translatedTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, NavFile, "groups:\n  - id: g\n    title: Guide\n    summary: The guide.\n    pages:\n      - path: g/start.md\n        title: Start\n        summary: Run it.\n      - path: ../CONTRIBUTING.md\n        title: Contributing\n        summary: Outside.\n")
	write(t, root, "CONTRIBUTING.md", "# Contributing\n")
	write(t, root, HubFile, "# Coddy documentation\n\nOne binary.\n\n<!-- docsgen:nav:start -->\n<!-- docsgen:nav:end -->\n")
	write(t, root, "docs/g/start.md", "# Start\n\nRun [it](#install).\n\n## Install\n\nDownload.\n\n### Details\n\nMore.\n")
	write(t, root, "docs/ru/nav.yaml", "groups:\n  g:\n    title: Руководство\n    summary: Руководство.\n    en:\n      title: Guide\n      summary: The guide.\npages:\n  g/start.md:\n    title: Начало\n    summary: Запустите.\n    en:\n      title: Start\n      summary: Run it.\n  ../CONTRIBUTING.md:\n    title: Участие\n    summary: Вне дерева.\n    en:\n      title: Contributing\n      summary: Outside.\n")
	ruStart := "# Начало\n\nЗапустите [это](#установка).\n\n## Установка\n\nСкачайте.\n\n### Подробности\n\nЕщё.\n"
	write(t, root, "docs/ru/g/start.md", ruStart+"\n"+StampLine(SourceDigest(readFile(t, root, "docs/g/start.md")))+"\n")
	ruHub := "# Документация Coddy\n\nОдин бинарник.\n\n<!-- docsgen:nav:start -->\n<!-- docsgen:nav:end -->\n"
	write(t, root, "docs/ru/README.md", ruHub+"\n"+StampLine(SourceDigest(readFile(t, root, HubFile)))+"\n")
	return root
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func checkRussian(t *testing.T, root string) []Problem {
	t.Helper()
	nav, err := LoadNav(root)
	if err != nil {
		t.Fatal(err)
	}
	return CheckTranslation(root, nav, "ru", nil)
}

func wantProblem(t *testing.T, problems []Problem, file, fragment string) {
	t.Helper()
	for _, p := range problems {
		if p.File == file && strings.Contains(p.Message, fragment) {
			return
		}
	}
	t.Fatalf("no problem %q on %s in:\n%v", fragment, file, problems)
}

func TestACurrentTranslationPasses(t *testing.T) {
	root := translatedTree(t)
	if problems := checkRussian(t, root); len(problems) != 0 {
		t.Fatalf("a current translation has problems: %v", problems)
	}
	nav, _ := LoadNav(root)
	// The Russian tree is the translation's to check: the map check of the
	// English tree does not report its pages.
	for _, p := range CheckNav(root, nav) {
		if strings.HasPrefix(p.File, "docs/ru/") {
			t.Fatalf("CheckNav reports the Russian tree: %v", p)
		}
	}
}

// The digest follows what the English page says, not what docsgen generates
// into it or how the checkout ends its lines.
func TestSourceDigestIgnoresGeneratedBlocksAndLineEndings(t *testing.T) {
	a := SourceDigest("# Page\n\nText.\n\n<!-- docsgen:config:start -->\n| old table |\n<!-- docsgen:config:end -->\n")
	b := SourceDigest("# Page\r\n\r\nText.  \r\n\r\n<!-- docsgen:config:start -->\r\n| new table |\r\n| row |\r\n<!-- docsgen:config:end -->\r\n\r\n")
	if a != b {
		t.Fatalf("digest changed with the generated block or the line endings: %s %s", a, b)
	}
	if c := SourceDigest("# Page\n\nOther text.\n"); c == a {
		t.Fatal("digest did not change with the text")
	}
	if len(a) != 16 {
		t.Fatalf("digest %q is not 16 hex characters", a)
	}
	if got := StampLine("0123456789abcdef"); got != "<!-- docsgen:source sha256=0123456789abcdef -->" {
		t.Fatalf("StampLine = %q", got)
	}
	if d, ok := ReadStamp("# P\n\ntext\n\n<!-- docsgen:source sha256=0123456789abcdef -->\n"); !ok || d != "0123456789abcdef" {
		t.Fatalf("ReadStamp = %q %v", d, ok)
	}
}

func TestAnEnglishChangeMakesItsTranslationStale(t *testing.T) {
	root := translatedTree(t)
	write(t, root, "docs/g/start.md", "# Start\n\nRun [it](#install) twice.\n\n## Install\n\nDownload.\n\n### Details\n\nMore.\n")
	problems := checkRussian(t, root)
	wantProblem(t, problems, "docs/ru/g/start.md", "translates an older docs/g/start.md")
	for _, p := range problems {
		if strings.Contains(p.Message, "docs-stamp") {
			t.Fatalf("the report offers the restamp as the next step: %v", p)
		}
	}
	// The hub's introduction is translated too.
	write(t, root, HubFile, "# Coddy documentation\n\nOne static binary.\n\n<!-- docsgen:nav:start -->\n<!-- docsgen:nav:end -->\n")
	wantProblem(t, checkRussian(t, root), "docs/ru/README.md", "translates an older docs/README.md")
}

func TestATranslationMustExistAndStartWithItsTitle(t *testing.T) {
	root := translatedTree(t)
	if err := os.Remove(filepath.Join(root, "docs/ru/g/start.md")); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, checkRussian(t, root), "docs/ru/g/start.md", "missing")

	root = translatedTree(t)
	write(t, root, "docs/ru/g/start.md", strings.Replace(readFile(t, root, "docs/ru/g/start.md"), "# Начало", "# Старт", 1))
	wantProblem(t, checkRussian(t, root), "docs/ru/g/start.md", "# Начало")

	root = translatedTree(t)
	write(t, root, "docs/ru/g/orphan.md", "# Сирота\n")
	write(t, root, "docs/ru/notes.txt", "x\n")
	problems := checkRussian(t, root)
	wantProblem(t, problems, "docs/ru/g/orphan.md", "no English page")
	wantProblem(t, problems, "docs/ru/notes.txt", "does not belong")

	root = translatedTree(t)
	write(t, root, "docs/ru/g/start.md", strings.TrimSuffix(strings.SplitAfter(readFile(t, root, "docs/ru/g/start.md"), "Ещё.\n")[0], ""))
	wantProblem(t, checkRussian(t, root), "docs/ru/g/start.md", "no stamp")
}

func TestTheRussianMapFollowsTheEnglishOne(t *testing.T) {
	root := translatedTree(t)
	write(t, root, NavFile, strings.Replace(readFile(t, root, NavFile), "summary: Run it.", "summary: Run it now.", 1))
	wantProblem(t, checkRussian(t, root), "docs/ru/nav.yaml", "g/start.md")

	root = translatedTree(t)
	write(t, root, NavFile, readFile(t, root, NavFile)+"      - path: g/more.md\n        title: More\n        summary: More.\n")
	write(t, root, "docs/g/more.md", "# More\n")
	problems := checkRussian(t, root)
	wantProblem(t, problems, "docs/ru/nav.yaml", "no entry for page g/more.md")
	wantProblem(t, problems, "docs/ru/g/more.md", "missing")
}

func TestHeadingsMustMatchTheSource(t *testing.T) {
	root := translatedTree(t)
	ru := readFile(t, root, "docs/ru/g/start.md")
	write(t, root, "docs/ru/g/start.md", strings.Replace(ru, "### Подробности", "## Подробности", 1))
	wantProblem(t, checkRussian(t, root), "docs/ru/g/start.md", "heading 3")

	root = translatedTree(t)
	write(t, root, "docs/ru/g/start.md", strings.Replace(ru, "### Подробности\n\nЕщё.\n", "", 1))
	wantProblem(t, checkRussian(t, root), "docs/ru/g/start.md", "headings")

	// A heading left in Latin script that spells another heading's shared
	// anchor could not be addressed by its own.
	root = translatedTree(t)
	write(t, root, "docs/ru/g/start.md", strings.Replace(ru, "### Подробности", "### Install", 1))
	wantProblem(t, checkRussian(t, root), "docs/ru/g/start.md", "#install")
}

func TestRussianTypographyOutsideCode(t *testing.T) {
	root := translatedTree(t)
	ru := readFile(t, root, "docs/ru/g/start.md")
	bad := strings.Replace(ru, "Скачайте.", "Скачайте — «архив», он агентский.\n\n---\n\nТак. `a — b` в коде можно.", 1)
	write(t, root, "docs/ru/g/start.md", bad)
	problems := checkRussian(t, root)
	for _, want := range []string{"em dash", "guillemet", "агентск", "horizontal rule"} {
		wantProblem(t, problems, "docs/ru/g/start.md", want)
	}
	for _, p := range problems {
		if strings.Contains(p.Message, "a — b") {
			t.Fatalf("inline code was checked: %v", p)
		}
	}
}

func TestConfigDescriptionsFollowTheSchema(t *testing.T) {
	schema := []byte(`{"type":"object","properties":{
	  "agent":{"type":"object","description":"The loop.","properties":{
	    "max_turns":{"type":"integer","description":"Cap on rounds."}}}}}`)
	root := translatedTree(t)
	write(t, root, "docs/ru/config-descriptions.yaml", "agent:\n  en: \"The loop.\"\n  ru: \"Цикл.\"\nagent.max_turns:\n  en: \"Cap on rounds.\"\n  ru: \"Предел ходов.\"\n")
	if problems := CheckConfigDescriptions(root, "ru", schema); len(problems) != 0 {
		t.Fatalf("current descriptions: %v", problems)
	}
	write(t, root, "docs/ru/config-descriptions.yaml", "agent:\n  en: \"The old loop.\"\n  ru: \"Цикл.\"\ngone.key:\n  en: \"x\"\n  ru: \"y\"\n")
	problems := CheckConfigDescriptions(root, "ru", schema)
	wantProblem(t, problems, "docs/ru/config-descriptions.yaml", "agent: the English description changed")
	wantProblem(t, problems, "docs/ru/config-descriptions.yaml", "agent.max_turns: no Russian description")
	wantProblem(t, problems, "docs/ru/config-descriptions.yaml", "gone.key: not a key of the schema")
}

func TestRussianConfigReferenceUsesTheTranslation(t *testing.T) {
	schema := `{"type":"object","properties":{
	  "logger":{"type":"object","description":"Logging.","properties":{
	    "level":{"type":"string","enum":["debug","info"],"description":"Verbosity."},
	    "outputs":{"type":"array","items":{"type":"string"},"description":"Sinks."}}},
	  "agent":{"type":"object","properties":{"max_turns":{"type":["integer","null"],"description":"Cap."}}}}}`
	ru := map[string]string{"logger": "Журнал.", "logger.level": "Подробность.", "logger.outputs": "Приёмники.", "agent.max_turns": "Предел."}
	out, err := ConfigReferenceLang([]byte(schema), map[string]string{"logger.level": "info"}, "ru", ru)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"### `logger`\n\nЖурнал.",
		"| Ключ | Тип | По умолчанию | Описание |",
		"| `logger.level` | строка, одно из `debug`, `info` | info | Подробность. |",
		"| `logger.outputs` | список строк |  | Приёмники. |",
		"| `agent.max_turns` | целое число или null |  | Предел. |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// The Russian changelog is derived from the English one, offline: a Russian
// title and introduction, the release notes as published.
func TestRussianChangelogKeepsTheReleaseNotes(t *testing.T) {
	en := "# Changelog\n\nRelease notes of every published version.\n\n## v1.2.0 - 2026-10-01\n\nFixed — a thing.\n\n[Release page](https://example/1).\n"
	ru := RussianChangelog(en)
	if !strings.HasPrefix(ru, "# История изменений\n\n") {
		t.Fatalf("title:\n%s", ru)
	}
	if !strings.HasSuffix(ru, "## v1.2.0 - 2026-10-01\n\nFixed — a thing.\n\n[Release page](https://example/1).\n") {
		t.Fatalf("release notes not kept as published:\n%s", ru)
	}
}

func TestRussianHubLinksRootPagesFromItsFolder(t *testing.T) {
	root := translatedTree(t)
	nav, err := LoadNav(root)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := LoadTranslatedNav(root, "ru")
	if err != nil {
		t.Fatal(err)
	}
	hub := RenderHubLang(nav, tr)
	for _, want := range []string{"## Руководство\n\nРуководство.\n", "- [Начало](g/start.md) - Запустите.", "- [Участие](../../CONTRIBUTING.md) - Вне дерева."} {
		if !strings.Contains(hub, want) {
			t.Fatalf("missing %q in the Russian hub:\n%s", want, hub)
		}
	}
}

// A stamp is written only over a page whose translation moved since HEAD,
// unless the caller says the English change needs no Russian one.
func TestStampRefusesAnUnchangedTranslation(t *testing.T) {
	root := translatedTree(t)
	gitInit(t, root)
	gitCommitAll(t, root)
	write(t, root, "docs/g/start.md", "# Start\n\nRun [it](#install) now.\n\n## Install\n\nDownload.\n\n### Details\n\nMore.\n")
	if _, err := Stamp(root, []string{"docs/ru/g/start.md"}, false); err == nil || !strings.Contains(err.Error(), "-unchanged") {
		t.Fatalf("an unchanged translation was stamped: %v", err)
	}
	if _, err := Stamp(root, []string{"docs/ru/g/start.md"}, true); err != nil {
		t.Fatalf("-unchanged: %v", err)
	}
	if problems := checkRussian(t, root); len(problems) != 0 {
		t.Fatalf("after the stamp: %v", problems)
	}
	write(t, root, "docs/g/start.md", "# Start\n\nRun [it](#install) at once.\n\n## Install\n\nDownload.\n\n### Details\n\nMore.\n")
	write(t, root, "docs/ru/g/start.md", strings.Replace(readFile(t, root, "docs/ru/g/start.md"), "Запустите", "Сразу запустите", 1))
	if _, err := Stamp(root, []string{"docs/ru/g/start.md"}, false); err != nil {
		t.Fatalf("a changed translation was refused: %v", err)
	}
	if _, err := Stamp(root, []string{"docs/g/start.md"}, false); err == nil {
		t.Fatal("an English page was stamped")
	}
}

func gitCommitAll(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "add", "-A"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "tree"},
	} {
		if out, err := gitCmd(root, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestSiteRendersTheRussianLayer(t *testing.T) {
	root := translatedTree(t)
	nav, err := LoadNav(root)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := LoadTranslatedNav(root, "ru")
	if err != nil {
		t.Fatal(err)
	}
	files, err := RenderSiteLang(root, nav, map[string]*TranslatedNav{"ru": tr}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	js := files[SiteRedirectScript]
	for _, want := range []string{`(?:([a-z]{2})\/)?docs`, `var LANGS = ["ru"];`, `"docs/" + (lang ? lang + "/" : "")`, "docs-anchors-"} {
		if !strings.Contains(js, want) {
			t.Errorf("redirect script lacks %q:\n%s", want, js)
		}
	}
	anchors := files["docs-anchors-ru.json"]
	if !strings.Contains(anchors, `"g/start":{"details":"подробности","install":"установка","start":"начало"}`) {
		t.Fatalf("anchors of the Russian pages: %s", anchors)
	}
	idx := files["ru/llms.txt"]
	if !strings.Contains(idx, "- [Начало](https://raw.githubusercontent.com/coddy-project/coddy-agent/main/docs/ru/g/start.md): Запустите.") {
		t.Fatalf("ru/llms.txt:\n%s", idx)
	}
	full := files["ru/llms-full.txt"]
	if !strings.Contains(full, "Скачайте.") || strings.Contains(full, "docsgen:source") {
		t.Fatalf("ru/llms-full.txt:\n%s", full)
	}
}

// The site layer is rendered from the tree as the run leaves it, under the
// raw base the run names: a Russian page whose links the run made right reads
// that way in ru/llms-full.txt, though the disk still holds the link as the
// translator copied it.
func TestSiteLayerFollowsTheRunsTree(t *testing.T) {
	root := translatedTree(t)
	write(t, root, ConfigRefFile, "# Config\n\n<!-- docsgen:"+MarkerConfig+":start -->\n<!-- docsgen:"+MarkerConfig+":end -->\n")
	write(t, root, AssetIndex, "# Assets\n\n<!-- docsgen:"+MarkerAssets+":start -->\n<!-- docsgen:"+MarkerAssets+":end -->\n")
	// The translator copied the English link; the run makes it Russian.
	write(t, root, "docs/ru/g/start.md", strings.Replace(readFile(t, root, "docs/ru/g/start.md"), "[это](#установка)", "[это](#install)", 1))
	const raw = "https://example.test/raw/branch"
	res, err := Generate(Options{Root: root, SkipCLI: true, SiteDir: t.TempDir(), RawBase: raw})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Files["docs/ru/g/start.md"], "[это](#установка)") {
		t.Fatalf("the run localizes the link:\n%s", res.Files["docs/ru/g/start.md"])
	}
	full := res.SiteFiles["ru/llms-full.txt"]
	for _, want := range []string{"Source: " + raw + "/docs/ru/g/start.md", "Запустите [это](#установка)."} {
		if !strings.Contains(full, want) {
			t.Errorf("ru/llms-full.txt lacks %q:\n%s", want, full)
		}
	}
	if strings.Contains(full, "(#install)") {
		t.Errorf("ru/llms-full.txt reads the page from the disk, not from the run:\n%s", full)
	}
	if idx := res.SiteFiles["ru/llms.txt"]; !strings.Contains(idx, "- [Начало]("+raw+"/docs/ru/g/start.md): Запустите.") {
		t.Errorf("ru/llms.txt under the run's raw base:\n%s", idx)
	}
	if anchors := res.SiteFiles["docs-anchors-ru.json"]; !strings.Contains(anchors, `"install":"установка"`) {
		t.Errorf("docs-anchors-ru.json:\n%s", anchors)
	}
}

// A missing translation is left to CheckTranslation; any other failure to
// read a page fails the render instead of shortening ru/llms-full.txt.
func TestLLMSFullLangFailsOnAnUnreadablePage(t *testing.T) {
	root := translatedTree(t)
	nav, err := LoadNav(root)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := LoadTranslatedNav(root, "ru")
	if err != nil {
		t.Fatal(err)
	}
	missing := func(rel string) (string, error) { return "", &fs.PathError{Op: "open", Path: rel, Err: fs.ErrNotExist} }
	if _, err := RenderLLMSFullLang(nav, tr, "ru", missing, DefaultRawBase); err != nil {
		t.Fatalf("a missing page is skipped: %v", err)
	}
	unreadable := func(rel string) (string, error) {
		return "", &fs.PathError{Op: "open", Path: rel, Err: fs.ErrPermission}
	}
	if _, err := RenderLLMSFullLang(nav, tr, "ru", unreadable, DefaultRawBase); err == nil || !strings.Contains(err.Error(), "docs/ru/g/start.md") {
		t.Fatalf("an unreadable page fails the render: %v", err)
	}
}

// A translator copies the links of the English page; make docs makes them
// right for the translation's folder.
func TestLocalizeLinksForTheTranslationFolder(t *testing.T) {
	root := translatedTree(t)
	write(t, root, "docs/assets/shot.png", "png")
	write(t, root, "docs/plans/design.md", "# Design\n")
	write(t, root, "internal/x.go", "package x\n")
	write(t, root, "README.md", "# Coddy\n\n## Install\n")
	ru := "# Начало\n\nСм. [выше](#install), [это](#установка), [страницу](start.md#details), [хаб](../README.md), [корень](../../README.md#install), [план](../plans/design.md), [код](../../internal/x.go), [`x.go`](../../internal/x.go), [сайт](https://coddy.dev/docs/x#install), `[код](#install)`.\n\n![снимок](../assets/shot.png)\n\n```\n[x](../assets/shot.png)\n```\n\n## Установка\n\nСкачайте.\n\n### Подробности\n\nЕщё.\n"
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(root, rel))
		return err == nil
	}
	read := func(rel string) (string, error) {
		if rel == "docs/ru/g/start.md" {
			return ru, nil
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		return string(b), err
	}
	got := localizeLinks("docs/ru/g/start.md", ru, read, exists)
	for _, want := range []string{
		"[выше](#установка)",
		"[это](#установка)",
		"[страницу](start.md#подробности)",
		"[хаб](../README.md)",
		"[корень](../../../README.md#install)",
		"[план](../../plans/design.md)",
		"[код](../../../internal/x.go)",
		"[`x.go`](../../../internal/x.go)",
		"[сайт](https://coddy.dev/docs/x#install)",
		"`[код](#install)`",
		"![снимок](../../assets/shot.png)",
		"[x](../assets/shot.png)\n```",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if again := localizeLinks("docs/ru/g/start.md", got, func(rel string) (string, error) {
		if rel == "docs/ru/g/start.md" {
			return got, nil
		}
		return read(rel)
	}, exists); again != got {
		t.Fatalf("not idempotent:\n%s", again)
	}
}
