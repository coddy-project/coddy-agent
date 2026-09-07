package subagents

import (
	"embed"
	"path"
	"sort"
	"strings"
)

// Built-in definitions ship with the binary so delegation works before the
// operator writes a single file. They sit below every directory: a user file
// with the same name replaces them.
//
//go:embed bundled/*.md
var bundledFS embed.FS

// Bundled returns fresh copies of the embedded definitions, sorted by name.
func Bundled() []*Definition {
	entries, err := bundledFS.ReadDir("bundled")
	if err != nil {
		return nil
	}
	out := make([]*Definition, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := bundledFS.ReadFile(path.Join("bundled", e.Name()))
		if err != nil {
			continue
		}
		def, err := Parse(e.Name(), data)
		if err != nil {
			// An embedded file is authored in this repository; a parse failure
			// is a build defect that the package tests catch.
			continue
		}
		def.Scope = ScopeBuiltin
		def.Builtin = true
		def.Path = ""
		out = append(out, def)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
