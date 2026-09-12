package docsgen

import (
	"fmt"
	"strings"
)

// Marker names. A generated block lives between "<!-- docsgen:NAME:start -->"
// and "<!-- docsgen:NAME:end -->" lines inside a hand-written file, so the
// prose around it survives regeneration.
const (
	MarkerNav    = "nav"
	MarkerConfig = "config"
	MarkerCLI    = "cli"
	MarkerAssets = "assets"
)

func startMarker(name string) string { return "<!-- docsgen:" + name + ":start -->" }
func endMarker(name string) string   { return "<!-- docsgen:" + name + ":end -->" }

// Splice replaces the block between the start and end markers of name in doc
// with body. The markers stay; body is placed on its own lines between them.
func Splice(doc, name, body string) (string, error) {
	start, end := startMarker(name), endMarker(name)
	i := strings.Index(doc, start)
	if i < 0 {
		return "", fmt.Errorf("marker %q not found", start)
	}
	j := strings.Index(doc[i:], end)
	if j < 0 {
		return "", fmt.Errorf("marker %q not found", end)
	}
	j += i
	body = strings.TrimRight(body, "\n")
	var b strings.Builder
	b.WriteString(doc[:i+len(start)])
	b.WriteString("\n")
	if body != "" {
		b.WriteString(body)
		b.WriteString("\n")
	}
	b.WriteString(doc[j:])
	return b.String(), nil
}
