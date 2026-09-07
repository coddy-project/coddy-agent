package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// frontmatter is the dialect-neutral view of a rule file's YAML header.
// Cursor calls the activation patterns globs and Claude Code calls them
// paths; both land in Globs. AlwaysApply stays nil when the key is absent,
// which is where the two dialects part (see classifyRule).
type frontmatter struct {
	Description string
	Globs       []string
	AlwaysApply *bool
}

// ParseRuleFile parses one rule file in the dialect its extension selects
// (.mdc is Cursor's, .md is Claude Code's). Files with any other extension
// are not rules and yield an error.
func ParseRuleFile(path string, src Source, data []byte) (*Rule, error) {
	format, ok := FormatForPath(path)
	if !ok {
		return nil, fmt.Errorf("rules: %s is neither a .md nor a .mdc file", path)
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	body, fm := parseFrontmatter(data)
	r := &Rule{
		ID:       string(src) + ":" + path,
		Name:     name,
		FilePath: path,
		Source:   src,
		Format:   format,
		Content:  strings.TrimSpace(body),
	}
	if fm != nil {
		r.Description = fm.Description
		r.Globs = append([]string(nil), fm.Globs...)
	}
	r.AlwaysApply, r.ApplyMode = classifyRule(format, fm)
	return r, nil
}

// classifyRule maps a header onto coddy's two activation modes.
//
// Both dialects agree on most of it: no frontmatter means the rule is on from
// the first turn; patterns mean it waits for the first matching file (Cursor's
// auto-attach and Claude Code's path scoping; coddy gates alwaysApply: true
// rules on their globs too, to keep the prompt small); an explicit alwaysApply
// is the master switch. They part on a header with neither: Cursor defaults
// alwaysApply to false, so the rule is manual (@mention, or agent-requested in
// Cursor itself), while Claude Code has no such key and loads the rule
// unconditionally.
func classifyRule(format Format, fm *frontmatter) (alwaysApply bool, mode ApplyMode) {
	if fm == nil || len(fm.Globs) > 0 {
		return true, ApplyAuto
	}
	if fm.AlwaysApply != nil {
		if *fm.AlwaysApply {
			return true, ApplyAuto
		}
		return false, ApplyMention
	}
	if format == FormatClaude {
		return true, ApplyAuto
	}
	return false, ApplyMention
}

// parseFrontmatter splits a YAML header off the body and returns a nil header
// when the file has none. The header is read as YAML first. Cursor's own
// files are frequently not valid YAML (a glob starting with "*" is a YAML
// alias), so a failed parse falls back to a line reader that knows the keys a
// rule header can carry.
func parseFrontmatter(data []byte) (string, *frontmatter) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		return string(data), nil
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return string(data), nil
	}
	header := lines[1:end]
	body := strings.Join(lines[end+1:], "\n")
	if fm, ok := parseFrontmatterYAML(strings.Join(header, "\n")); ok {
		return body, fm
	}
	return body, parseFrontmatterLoose(header)
}

// yamlHeader is the strict reading of a rule header.
type yamlHeader struct {
	Description string     `yaml:"description"`
	Globs       globList   `yaml:"globs"`
	Paths       globList   `yaml:"paths"`
	AlwaysApply *looseBool `yaml:"alwaysApply"`
}

func parseFrontmatterYAML(src string) (*frontmatter, bool) {
	var h yamlHeader
	if err := yaml.Unmarshal([]byte(src), &h); err != nil {
		return nil, false
	}
	fm := &frontmatter{Description: strings.TrimSpace(h.Description)}
	fm.Globs = append(fm.Globs, h.Globs...)
	fm.Globs = append(fm.Globs, h.Paths...)
	if h.AlwaysApply != nil {
		v := bool(*h.AlwaysApply)
		fm.AlwaysApply = &v
	}
	return fm, true
}

// globList accepts the pattern list in every shape the dialects use: one
// comma-separated string (Cursor), a block or flow sequence (Claude Code, and
// Cursor files written by hand).
type globList []string

func (g *globList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!null" {
			return nil
		}
		*g = append(*g, splitGlobs(n.Value)...)
	case yaml.SequenceNode:
		for _, item := range n.Content {
			if item.Kind != yaml.ScalarNode {
				return fmt.Errorf("globs: nested values are not supported")
			}
			*g = append(*g, splitGlobs(item.Value)...)
		}
	default:
		return fmt.Errorf("globs: expected a string or a list")
	}
	return nil
}

// looseBool reads alwaysApply the way people write it: true/false, yes/no,
// on/off, 1/0, quoted or not.
type looseBool bool

func (b *looseBool) UnmarshalYAML(n *yaml.Node) error {
	v, ok := parseLooseBool(n.Value)
	if !ok {
		return fmt.Errorf("alwaysApply: %q is not a boolean", n.Value)
	}
	*b = looseBool(v)
	return nil
}

func parseLooseBool(s string) (value, ok bool) {
	switch strings.ToLower(strings.TrimSpace(unquote(s))) {
	case "true", "yes", "on", "1":
		return true, true
	case "false", "no", "off", "0":
		return false, true
	}
	return false, false
}

// parseFrontmatterLoose reads "key: value" lines the way Cursor's own reader
// does, without YAML. A list is either comma-separated on the key's line, a
// [flow, list], or "- item" lines below an empty key. Unknown keys are skipped.
func parseFrontmatterLoose(lines []string) *frontmatter {
	fm := &frontmatter{}
	for i := 0; i < len(lines); i++ {
		key, value, ok := splitHeaderLine(lines[i])
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "description":
			fm.Description = unquote(stripComment(value))
		case "globs", "paths":
			if strings.TrimSpace(value) == "" {
				for i+1 < len(lines) {
					item, ok := listItem(lines[i+1])
					if !ok {
						break
					}
					fm.Globs = append(fm.Globs, splitGlobs(item)...)
					i++
				}
				continue
			}
			v := stripComment(value)
			v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
			fm.Globs = append(fm.Globs, splitGlobs(v)...)
		case "alwaysapply":
			if b, ok := parseLooseBool(stripComment(value)); ok {
				fm.AlwaysApply = &b
			}
		}
	}
	return fm
}

// splitHeaderLine returns the key and raw value of a top-level "key: value"
// line. Indented lines, list items and comments are not keys.
func splitHeaderLine(line string) (key, value string, ok bool) {
	if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '-' || line[0] == '#' {
		return "", "", false
	}
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	for _, r := range key {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return "", "", false
		}
	}
	return key, line[idx+1:], true
}

// listItem returns the value of a "- item" line.
func listItem(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "-") {
		return "", false
	}
	if len(t) > 1 && t[1] != ' ' && t[1] != '\t' {
		return "", false
	}
	return stripComment(t[1:]), true
}

// stripComment drops a trailing " # comment" and surrounding space.
func stripComment(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "#") {
		return ""
	}
	if i := strings.Index(t, " #"); i >= 0 {
		t = t[:i]
	}
	return strings.TrimSpace(t)
}

// unquote removes one pair of matching single or double quotes.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// splitGlobs splits a comma-separated pattern list without breaking brace
// groups such as *.{ts,tsx}, and drops quotes, surrounding space and empties.
func splitGlobs(s string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	flush := func() {
		if p := unquote(cur.String()); p != "" {
			out = append(out, p)
		}
		cur.Reset()
	}
	for _, r := range s {
		switch r {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				flush()
				continue
			}
		}
		cur.WriteRune(r)
	}
	flush()
	return out
}

func loadMarkdownRulesFromRoot(root string, src Source) ([]*Rule, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, os.ErrNotExist
	}
	var out []*Rule
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if _, ok := FormatForPath(d.Name()); !ok {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		r, err := ParseRuleFile(path, src, data)
		if err != nil {
			return nil
		}
		out = append(out, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MatchGlob reports whether file matches pattern. Patterns use doublestar
// syntax (**, {a,b}, [abc]) and are anchored at the project root: an absolute
// file is matched by its path relative to root, a relative file is taken as
// already root-relative, and a file outside the root (or on another volume)
// matches nothing, so neither the host path above the workspace nor a
// sibling checkout can activate a rule. Without a root the file is matched as
// given. As in Cursor and Claude Code, a pattern without a directory part
// ("*.md") names files in the root only; "**/*.md" reaches every depth.
func MatchGlob(pattern, root, file string) bool {
	pattern = strings.TrimPrefix(strings.TrimSpace(filepath.ToSlash(pattern)), "./")
	file = strings.TrimSpace(file)
	if pattern == "" || file == "" {
		return false
	}
	candidate := filepath.Clean(file)
	if root != "" && filepath.IsAbs(candidate) {
		rel, err := filepath.Rel(root, candidate)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false
		}
		candidate = rel
	}
	return globMatch(pattern, filepath.ToSlash(candidate))
}

func globMatch(pattern, candidate string) bool {
	if runtime.GOOS == "windows" {
		pattern, candidate = strings.ToLower(pattern), strings.ToLower(candidate)
	}
	ok, err := doublestar.Match(pattern, candidate)
	return err == nil && ok
}

func matchesRuleGlobs(r *Rule, contextFiles []string) bool {
	if r == nil || len(r.Globs) == 0 {
		return false
	}
	for _, p := range r.Globs {
		for _, f := range contextFiles {
			if MatchGlob(p, r.Root, f) {
				return true
			}
		}
	}
	return false
}
