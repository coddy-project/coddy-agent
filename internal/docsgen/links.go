package docsgen

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Problem is one finding of a check: the file it concerns and what is wrong.
type Problem struct {
	File    string
	Message string
}

func (p Problem) String() string { return p.File + ": " + p.Message }

// linkRE matches markdown links and images "](target)", reference-style
// definitions "[name]: target" and HTML "src=" or "href=" attributes. Only
// the target is captured.
var linkRE = regexp.MustCompile(`(?m)(?:\]\(|(?:src|href)=")([^)"\s]+)[)"]|^\[[^\]\n]+\]:[ \t]+(\S+)`)

// fencedRE strips fenced code blocks so a "](x)" inside an example is not a
// link. A fence opens and closes at the start of a line, with backticks or
// tildes: three backticks in running prose are not a fence.
var fencedRE = regexp.MustCompile("(?ms)^[ \\t]*(```|~~~).*?^[ \\t]*(```|~~~)[ \\t]*$")

// inlineCodeRE strips inline code spans for the same reason.
var inlineCodeRE = regexp.MustCompile("`[^`\n]*`")

// CheckLinks verifies that every relative link and image in the given
// markdown files (paths relative to root) points at an existing file, and
// that a "#fragment" on a markdown target names a heading of that file.
// Absolute URLs, mailto: and bare fragments are not checked.
func CheckLinks(root string, files []string) []Problem {
	var problems []Problem
	headings := map[string]map[string]bool{}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			problems = append(problems, Problem{rel, err.Error()})
			continue
		}
		text := inlineCodeRE.ReplaceAllString(fencedRE.ReplaceAllString(string(data), ""), "")
		dir := filepath.Dir(rel)
		self := filepath.ToSlash(filepath.Join(root, rel))
		for _, m := range linkRE.FindAllStringSubmatch(text, -1) {
			target := m[1]
			if target == "" {
				target = m[2]
			}
			if strings.HasPrefix(target, "{{") || strings.HasPrefix(target, "<") {
				continue
			}
			if strings.HasPrefix(target, "#") {
				// A fragment of this very page.
				if headings[self] == nil {
					headings[self] = headingAnchors(self)
				}
				if !headings[self][strings.ToLower(target[1:])] {
					problems = append(problems, Problem{rel, "link " + target + ": no heading with that anchor on this page"})
				}
				continue
			}
			if u, err := url.Parse(target); err == nil && u.Scheme != "" {
				continue
			}
			if strings.HasPrefix(target, "/") {
				continue // a route or an absolute path, not a file of the tree
			}
			path, frag, _ := strings.Cut(target, "#")
			path, _ = url.PathUnescape(path)
			abs := filepath.Clean(filepath.Join(root, dir, path))
			if _, err := os.Stat(abs); err != nil {
				problems = append(problems, Problem{rel, "broken link " + target})
				continue
			}
			if frag == "" || !strings.HasSuffix(strings.ToLower(path), ".md") {
				continue
			}
			key := filepath.ToSlash(abs)
			if headings[key] == nil {
				headings[key] = headingAnchors(abs)
			}
			if !headings[key][strings.ToLower(frag)] {
				problems = append(problems, Problem{rel, fmt.Sprintf("link %s: no heading with anchor #%s in %s", target, frag, path)})
			}
		}
	}
	sort.Slice(problems, func(i, j int) bool { return problems[i].String() < problems[j].String() })
	return problems
}

// headingAnchors returns the GitHub-style anchors of every heading in a file.
func headingAnchors(path string) map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	text := fencedRE.ReplaceAllString(string(data), "")
	if !strings.Contains(text, "\n#") && !strings.HasPrefix(text, "#") {
		return out
	}
	seen := map[string]int{}
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		title := strings.TrimLeft(line, "#")
		if !strings.HasPrefix(title, " ") {
			continue
		}
		slug := Slug(strings.TrimSpace(title))
		if n := seen[slug]; n > 0 {
			out[fmt.Sprintf("%s-%d", slug, n)] = true
		} else {
			out[slug] = true
		}
		seen[slug]++
	}
	return out
}

// Slug converts a heading to the anchor GitHub generates for it: inline
// markup removed, lower-cased, punctuation dropped, spaces turned into hyphens.
func Slug(heading string) string {
	// Drop inline markup that GitHub does not render into the anchor.
	r := strings.NewReplacer("`", "", "*", "", "[", "", "]", "", "(", "", ")", "")
	h := strings.ToLower(r.Replace(heading))
	var b strings.Builder
	for _, c := range h {
		switch {
		case unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '-':
			b.WriteRune(c)
		case c == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}
