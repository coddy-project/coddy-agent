package rules

import (
	"strings"
)

// RenderPrompt builds the {{.Rules}} markdown block - the project docs
// preamble, then the always-on rules - and reports the absolute paths of the
// project docs it embedded, so the caller can keep a file that is already in
// this block out of the instructions block instead of sending the same bytes
// twice. A rule gated by a path is never rendered here: it arrives with the
// tool result or the message that brought its path into play.
func RenderPrompt(home, cwd string, alwaysOn []*Rule) (string, []string) {
	var parts []string
	var embedded []string
	docs := LoadProjectDocs(home, cwd)
	for _, d := range docs {
		embedded = append(embedded, d.Path)
		var b strings.Builder
		b.WriteString("### ")
		b.WriteString(d.Label)
		b.WriteString("\n\n")
		b.WriteString(d.Content)
		parts = append(parts, b.String())
	}
	if section := RenderSection("## Active project rules", alwaysOn); section != "" {
		parts = append(parts, section)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")), embedded
}

// RenderSection renders rules under the given markdown heading, skipping nil
// entries and repeats of a rule already written. It returns an empty string when
// nothing is left to write, so a caller can append the result unconditionally.
func RenderSection(heading string, rs []*Rule) string {
	seen := make(map[string]struct{}, len(rs))
	var dyn []*Rule
	for _, r := range rs {
		if r == nil {
			continue
		}
		if _, ok := seen[r.ID]; ok {
			continue
		}
		seen[r.ID] = struct{}{}
		dyn = append(dyn, r)
	}
	if len(dyn) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(heading)
	b.WriteString("\n\n")
	for _, r := range dyn {
		head := r.CanonicalName()
		if r.Description != "" {
			b.WriteString("### ")
			b.WriteString(head)
			b.WriteString(" (")
			b.WriteString(r.Description)
			b.WriteString(")\n\n")
		} else {
			b.WriteString("### ")
			b.WriteString(head)
			b.WriteString("\n\n")
		}
		b.WriteString(r.Content)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}
