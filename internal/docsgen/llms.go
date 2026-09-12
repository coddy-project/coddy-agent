package docsgen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
