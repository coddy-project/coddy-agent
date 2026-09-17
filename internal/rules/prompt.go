package rules

import (
	"strings"
)

// RenderPrompt builds the {{.Rules}} markdown block and reports the absolute
// paths of the project docs it embedded, so the caller can keep a file that is
// already in this block out of the instructions block instead of sending the
// same bytes twice.
func RenderPrompt(home, cwd string, stickyAuto, mentioned []*Rule) (string, []string) {
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
	if section := RenderSection("## Active project rules", append(append([]*Rule(nil), stickyAuto...), mentioned...)); section != "" {
		parts = append(parts, section)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")), embedded
}

// RenderSection renders rules under the given markdown heading, skipping nil
// entries and repeats of a rule already written. It returns an empty string when
// nothing is left to write, so a caller can append the result unconditionally.
// The system prompt uses it for the rules a turn starts with; the turn context
// block uses it for the ones a tool call activated after that.
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

// Added reports the rules present in now but not in before, in the order now
// holds them. It is how a turn tells the rules a tool call activated apart from
// the ones the frozen system prompt already carries.
func Added(before, now []*Rule) []*Rule {
	had := make(map[string]struct{}, len(before))
	for _, r := range before {
		if r != nil {
			had[r.ID] = struct{}{}
		}
	}
	var out []*Rule
	for _, r := range now {
		if r == nil {
			continue
		}
		if _, ok := had[r.ID]; ok {
			continue
		}
		out = append(out, r)
	}
	return out
}
