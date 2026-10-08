package docsgen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/docs"
)

// DefaultRawBase is where the Markdown of the main branch is served from.
const DefaultRawBase = GitHubRaw

// rawURL returns the raw address of a nav page under base.
func rawURL(base, navPath string) string {
	return strings.TrimRight(base, "/") + "/" + RepoPath(navPath)
}

// mdLinkRE matches an inline markdown link; the blockquote keeps its text only.
var mdLinkRE = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)

// hubIntro returns the first paragraph of docs/README.md after the H1, with
// links reduced to their text: it becomes the blockquote of llms.txt. The
// later paragraphs of the hub talk about the hub itself and stay out.
func hubIntro(hub string) []string {
	i := strings.Index(hub, startMarker(MarkerNav))
	if i >= 0 {
		hub = hub[:i]
	}
	for _, l := range strings.Split(hub, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "# ") || strings.HasPrefix(l, "<!--") {
			continue
		}
		return []string{mdLinkRE.ReplaceAllString(l, "$1")}
	}
	return nil
}

// RenderLLMSIndex renders llms.txt (https://llmstxt.org): a title, a
// blockquote summary, then one section per group with a link and a
// one-line description per page.
func RenderLLMSIndex(nav *Nav, hub, base string) string {
	var b strings.Builder
	b.WriteString("# Coddy Agent\n\n")
	for _, l := range hubIntro(hub) {
		fmt.Fprintf(&b, "> %s\n", l)
	}
	b.WriteString("\nEvery page below is the Markdown kept in the repository, read straight from the main branch, so it always matches the code; the same page for people is https://coddy.dev/docs/<path without .md>. llms-full.txt next to this file holds the same pages concatenated. Source: https://github.com/coddy-project/coddy-agent\n")
	b.WriteString("\n## Website\n\n")
	b.WriteString("- [Install](https://coddy.dev/#install): one command per platform, the packages and the Docker image.\n")
	b.WriteString("- [Compare](https://coddy.dev/compare/): Coddy against other agent harnesses, including where it loses.\n")
	b.WriteString("- [config.yaml JSON Schema](https://coddy.dev/config.schema.json): the schema Coddy writes as a modeline into every config it saves.\n")
	b.WriteString("- [Russian documentation](https://coddy.dev/ru/llms.txt): the same pages translated into Russian, under docs/ru/ in the repository.\n")
	for _, g := range nav.Groups {
		fmt.Fprintf(&b, "\n## %s\n\n", g.Title)
		for _, p := range g.Pages {
			// The repository path follows the summary so an agent with a checkout
			// can open the file without the network, and so the entry stays
			// meaningful if the raw address ever moves.
			fmt.Fprintf(&b, "- [%s](%s): %s (%s)\n", p.Title, rawURL(base, p.Path), p.Summary, RepoPath(p.Path))
		}
	}
	return b.String()
}

// RenderLLMSFull concatenates every page in map order, each under a heading
// that names its group and title and a line with its source address.
func RenderLLMSFull(nav *Nav, root, base string) (string, error) {
	var b strings.Builder
	b.WriteString("# Coddy Agent, full documentation\n\n")
	b.WriteString("Generated from docs/nav.yaml by make docs. Every section is one page of the repository documentation in reading order; the source line names the file.\n")
	for _, g := range nav.Groups {
		for _, p := range g.Pages {
			data, err := os.ReadFile(filepath.Join(root, RepoPath(p.Path)))
			if os.IsNotExist(err) {
				continue // CheckNav reports the missing page
			}
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "\n\n# %s / %s\n\nSource: %s\n\n", g.Title, p.Title, rawURL(base, p.Path))
			b.WriteString(strings.TrimRight(string(data), "\n"))
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// llmsText are the fixed parts of a translated llms.txt and llms-full.txt.
type llmsText struct {
	title, note, fullTitle, fullNote, source string
}

var llmsTexts = map[string]llmsText{
	docs.Russian: {
		title:     "# Coddy Agent - документация на русском",
		note:      "Каждая страница ниже - перевод документации из репозитория, это Markdown прямо из ветки main, поэтому он всегда соответствует коду. Английский оригинал описан в https://coddy.dev/llms.txt, а страница для людей открывается по адресу https://coddy.dev/ru/docs/<путь без .md>. Все страницы подряд собраны в llms-full.txt рядом с этим файлом. Исходники - https://github.com/coddy-project/coddy-agent",
		fullTitle: "# Coddy Agent, вся документация на русском",
		fullNote:  "Собрано из docs/nav.yaml и docs/ru/ командой make site-docs. Каждый раздел - одна страница документации в порядке чтения; строка Source называет файл.",
		source:    "Source",
	},
}

// RenderLLMSIndexLang renders <lang>/llms.txt: the translated titles and
// summaries, each page at its translation's raw address, a page outside
// docs/ at its English one.
func RenderLLMSIndexLang(nav *Nav, tr *TranslatedNav, lang, hub, base string) string {
	text := llmsTexts[lang]
	local := tr.localize(nav)
	var b strings.Builder
	b.WriteString(text.title + "\n\n")
	for _, l := range hubIntro(stampRE.ReplaceAllString(hub, "")) {
		fmt.Fprintf(&b, "> %s\n", l)
	}
	b.WriteString("\n" + text.note + "\n")
	for _, g := range local.Groups {
		fmt.Fprintf(&b, "\n## %s\n\n", g.Title)
		for _, p := range g.Pages {
			path := translatedPath(lang, p.Path)
			fmt.Fprintf(&b, "- [%s](%s): %s (%s)\n", p.Title, strings.TrimRight(base, "/")+"/"+path, p.Summary, path)
		}
	}
	return b.String()
}

// RenderLLMSFullLang concatenates the translated pages in map order, their
// stamps removed; a page outside docs/ is left out, as it is not translated.
func RenderLLMSFullLang(nav *Nav, tr *TranslatedNav, lang, root, base string) (string, error) {
	text := llmsTexts[lang]
	local := tr.localize(nav)
	var b strings.Builder
	b.WriteString(text.fullTitle + "\n\n" + text.fullNote + "\n")
	for _, g := range local.Groups {
		for _, p := range g.Pages {
			if strings.HasPrefix(p.Path, "../") {
				continue
			}
			path := translatedPath(lang, p.Path)
			data, err := os.ReadFile(filepath.Join(root, path))
			if os.IsNotExist(err) {
				continue // CheckTranslation reports the missing page
			}
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "\n\n# %s / %s\n\n%s: %s\n\n", g.Title, p.Title, text.source, strings.TrimRight(base, "/")+"/"+path)
			b.WriteString(strings.TrimRight(stampRE.ReplaceAllString(string(data), ""), "\n"))
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// translatedPath is the repository path of a nav page in a language: the
// translation for a page under docs/, the English file for a root page.
func translatedPath(lang, navPath string) string {
	if strings.HasPrefix(navPath, "../") {
		return RepoPath(navPath)
	}
	return TranslationDir(lang) + filepath.ToSlash(filepath.Clean(navPath))
}
