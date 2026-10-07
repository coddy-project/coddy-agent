package rules

import (
	"strings"
)

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
