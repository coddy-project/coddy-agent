package plans

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const frontmatterDelim = "---"

// Parse reads a .plan.md file into a Document.
func Parse(slug, raw string) (*Document, error) {
	if err := ValidateSlug(slug); err != nil {
		return nil, err
	}
	fm, body, err := splitFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	var meta Frontmatter
	if strings.TrimSpace(fm) != "" {
		if err := yaml.Unmarshal([]byte(fm), &meta); err != nil {
			return nil, fmt.Errorf("plan frontmatter: %w", err)
		}
	}
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = defaultPlanName
	}
	return &Document{
		Slug:     slug,
		Name:     name,
		Overview: strings.TrimSpace(meta.Overview),
		Todos:    []TodoItem(meta.Todos),
		Body:     strings.TrimRight(body, "\n"),
		Content:  raw,
	}, nil
}

// Format builds file content from parts.
func Format(fm Frontmatter, body string) string {
	var buf bytes.Buffer
	buf.WriteString(frontmatterDelim)
	buf.WriteByte('\n')
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(fm)
	_ = enc.Close()
	buf.WriteString(frontmatterDelim)
	buf.WriteByte('\n')
	body = strings.TrimRight(body, "\n")
	if body != "" {
		buf.WriteString(body)
		buf.WriteByte('\n')
	}
	return buf.String()
}

// DefaultContent returns starter content for a new plan.
func DefaultContent(slug, name string) string {
	n := strings.TrimSpace(name)
	if n == "" {
		n = defaultPlanName
	}
	return Format(Frontmatter{
		Name:     n,
		Overview: "",
		Todos:    nil,
	}, "## Summary\n\n(Describe the goal.)\n\n## Steps\n\n1. \n")
}

func splitFrontmatter(raw string) (yamlPart, body string, err error) {
	raw = strings.TrimPrefix(raw, "\uFEFF")
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, frontmatterDelim) {
		return "", strings.TrimRight(raw, "\n"), nil
	}
	rest := strings.TrimPrefix(s, frontmatterDelim)
	rest = strings.TrimLeft(rest, "\r\n")
	idx := strings.Index(rest, "\n"+frontmatterDelim)
	if idx < 0 {
		return "", "", fmt.Errorf("plan frontmatter: missing closing %s", frontmatterDelim)
	}
	yamlPart = strings.TrimSpace(rest[:idx])
	body = strings.TrimLeft(rest[idx+len(frontmatterDelim):], "\r\n")
	return yamlPart, body, nil
}
