package docsgen

import (
	"fmt"
	"strings"
)

// RenderHub renders the navigation block of docs/README.md: one H2 per group
// with its summary and a bullet per page, links relative to docs/.
func RenderHub(nav *Nav) string {
	var b strings.Builder
	for i, g := range nav.Groups {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "## %s\n\n", g.Title)
		if g.Summary != "" {
			fmt.Fprintf(&b, "%s\n\n", g.Summary)
		}
		for _, p := range g.Pages {
			fmt.Fprintf(&b, "- [%s](%s) - %s\n", p.Title, p.Path, p.Summary)
		}
	}
	return b.String()
}
