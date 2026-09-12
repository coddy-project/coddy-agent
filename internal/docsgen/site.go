package docsgen

import (
	"fmt"
	"html"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The documentation layer of coddy.dev. Every page of the map has a stable
// address there: coddy.dev/docs/<slug> is a redirect page that sends a person
// to the rendered page on GitHub (the fragment survives), and
// coddy.dev/docs/<slug>.md is the Markdown itself, for agents and for
// llms.txt, which lives at the site root with llms-full.txt. The binary, the
// schema and the bundled skill print the stable addresses, so a page can move
// in the repository without changing what users were told.
const (
	SiteBase   = "https://coddy.dev/docs/"
	GitHubBlob = "https://github.com/coddy-project/coddy-agent/blob/main/"
	GitHubRaw  = "https://raw.githubusercontent.com/coddy-project/coddy-agent/main/"
)

// SiteSlug is the address of a nav page under coddy.dev/docs/: the path
// without its extension; a page outside docs/ (../CONTRIBUTING.md) is known
// by its file name.
func SiteSlug(navPath string) string {
	p := strings.TrimSuffix(filepath.ToSlash(navPath), ".md")
	if strings.HasPrefix(p, "../") {
		return path.Base(p)
	}
	return p
}

// SitePageURL is the Markdown twin of a nav page on the site.
func SitePageURL(navPath string) string { return SiteBase + SiteSlug(navPath) + ".md" }

// SiteRedirectURL is the human address of a nav page on the site.
func SiteRedirectURL(navPath string) string { return SiteBase + SiteSlug(navPath) }

// githubPageURL is where the redirect page sends a person.
func githubPageURL(navPath string) string { return GitHubBlob + RepoPath(navPath) }

// RenderSite renders the files of the documentation layer, keyed by their
// path relative to the site checkout. pages overlays the repository files
// with content generated in the same run.
func RenderSite(nav *Nav, root string, pages map[string]string) (map[string]string, error) {
	out := map[string]string{}
	out["docs/index.html"] = redirectPage("Coddy documentation", GitHubBlob+"docs/README.md", "README.md")
	for _, g := range nav.Groups {
		for _, p := range g.Pages {
			rel := RepoPath(p.Path)
			content, ok := pages[rel]
			if !ok {
				data, err := os.ReadFile(filepath.Join(root, rel))
				if os.IsNotExist(err) {
					continue // CheckNav reports it
				}
				if err != nil {
					return nil, err
				}
				content = string(data)
			}
			slug := SiteSlug(p.Path)
			out["docs/"+slug+"/index.html"] = redirectPage(p.Title, githubPageURL(p.Path), slug+".md")
			out["docs/"+slug+".md"] = twinContent(rel, content)
		}
	}
	return out, nil
}

// redirectPage is a static page that forwards to GitHub and keeps the
// fragment, with the Markdown twin one link away.
func redirectPage(title, target, twin string) string {
	t := html.EscapeString(title)
	u := html.EscapeString(target)
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>%s</title>
<meta name="robots" content="noindex">
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="canonical" href="%s">
<meta http-equiv="refresh" content="0; url=%s">
<script>location.replace(%q + location.hash);</script>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;font-family:system-ui,-apple-system,"Segoe UI",sans-serif;background:#121212;color:#fff}p{max-width:36rem;padding:2rem;line-height:1.6}a{color:#c084fc}</style>
</head>
<body>
<p>Taking you to <a href="%s">%s</a> on GitHub. The Markdown of this page, for agents and scripts, is <a href="%s">%s</a>.</p>
</body>
</html>
`, t, u, u, target, u, t, html.EscapeString(twin), html.EscapeString(twin))
}

var twinLinkRE = regexp.MustCompile(`(\]\(|(?:src|href)=")([^)"\s#]+)(#[^)"\s]*)?([)"])`)

// twinContent rewrites the relative links of a page for its copy on the
// site: images and everything outside docs/ point at GitHub, other pages
// stay relative because their twins sit at the same relative places.
func twinContent(rel, content string) string {
	dir := path.Dir(rel)
	return twinLinkRE.ReplaceAllStringFunc(content, func(m string) string {
		parts := twinLinkRE.FindStringSubmatch(m)
		opener, target, frag, closer := parts[1], parts[2], parts[3], parts[4]
		if u, err := url.Parse(target); err == nil && u.Scheme != "" {
			return m
		}
		if strings.HasPrefix(target, "/") || strings.HasPrefix(target, "{{") {
			return m
		}
		resolved := path.Clean(path.Join(dir, target))
		switch {
		case strings.HasPrefix(resolved, "docs/assets/"):
			return opener + GitHubRaw + resolved + frag + closer
		case strings.HasPrefix(resolved, "docs/") && strings.HasSuffix(resolved, ".md"):
			return m // another twin, same relative place
		case strings.HasPrefix(resolved, "docs/"):
			return opener + GitHubBlob + resolved + frag + closer
		default:
			return opener + GitHubBlob + resolved + frag + closer
		}
	})
}

// SiteStale compares the rendered site files with a checkout.
func SiteStale(siteDir string, files map[string]string) []Problem {
	var out []Problem
	for rel, content := range files {
		data, err := os.ReadFile(filepath.Join(siteDir, rel))
		if err != nil || strings.TrimRight(string(data), "\n") != strings.TrimRight(content, "\n") {
			out = append(out, Problem{"site:" + rel, "stale or missing on the site, run make site-docs"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

// WriteSite stores the site files under siteDir.
func WriteSite(siteDir string, files map[string]string) error {
	for rel, content := range files {
		p := filepath.Join(siteDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}
