package rules

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

// ListCatalog prints discovered rules for CLI.
func ListCatalog(cwd string, f *Factory, systems []Source) error {
	return RenderCatalog(os.Stdout, cwd, f, systems)
}

// RenderCatalog writes the discovered rules as the table `coddy rules list`
// prints: one row per rule with its source folder, the dialect its extension
// selected, the activation mode, whether it is in every prompt (ALWAYS: an
// auto rule with no patterns and no directory scope) and what activates it.
func RenderCatalog(w io.Writer, cwd string, f *Factory, systems []Source) error {
	if f == nil {
		f = DefaultFactory()
	}
	rules, err := f.Discover(cwd, systems)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		_, err := fmt.Fprintln(w, "No rules found.")
		return err
	}
	t := table.NewWriter()
	t.SetOutputMirror(w)
	t.AppendHeader(table.Row{"SOURCE", "FORMAT", "NAME", "APPLY", "ALWAYS", "ACTIVATES ON", "DESCRIPTION"})
	for _, r := range rules {
		// A directory-scoped rule (nested AGENTS.md) has no globs: what gates it
		// is its own subtree, so show that instead of an empty column.
		activates := strings.Join(r.Globs, ", ")
		if r.ScopeDir != "" {
			activates = scopeDirLabel(cwd, r.ScopeDir) + "/**"
		}
		if len(activates) > 60 {
			activates = activates[:57] + "..."
		}
		desc := r.Description
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}
		// ALWAYS answers "is this rule in every prompt?": a gated rule is an
		// auto rule too, but it waits for a matching path.
		alwaysOn := r.ApplyMode == ApplyAuto && len(r.Globs) == 0 && r.ScopeDir == ""
		t.AppendRow(table.Row{
			string(r.Source),
			string(r.Format),
			r.CanonicalName(),
			string(r.ApplyMode),
			fmt.Sprintf("%v", alwaysOn),
			activates,
			desc,
		})
	}
	style := table.StyleRounded
	style.Format.Header = text.FormatUpper
	t.SetStyle(style)
	t.Render()
	_, err = fmt.Fprintf(w, "\n%d rule(s) under %s\n", len(rules), cwd)
	return err
}

// scopeDirLabel renders a rule's ScopeDir relative to cwd, slash-separated.
func scopeDirLabel(cwd, scopeDir string) string {
	rel, err := filepath.Rel(cwd, scopeDir)
	if err != nil {
		return filepath.ToSlash(scopeDir)
	}
	if rel == "." {
		return "."
	}
	return filepath.ToSlash(rel)
}
