// Package docs carries the documentation tree into the binary: the map
// (nav.yaml) and every page of its groups. Design records (plans/), the
// assets and the generated llms files stay out, and a page the map lists
// outside this directory (../CONTRIBUTING.md) is not carried either.
// internal/docs is what reads it; nothing else should.
package docs

import "embed"

// FS holds nav.yaml and the Markdown pages of the documentation groups. A
// new group directory has to be added to the pattern below: the test of
// internal/docs fails when a page of the map is missing from it.
//
//go:embed nav.yaml getting-started/*.md surfaces/*.md operate/*.md features/*.md reference/*.md tutorials/*.md contributing/*.md
var FS embed.FS
