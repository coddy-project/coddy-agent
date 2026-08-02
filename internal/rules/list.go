package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

// ListCatalog prints discovered rules for CLI.
func ListCatalog(cwd string, f *Factory, systems []Source) error {
	if f == nil {
		f = DefaultFactory()
	}
	rules, err := f.Discover(cwd, systems)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		fmt.Println("No rules found.")
		return nil
	}
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"SOURCE", "NAME", "APPLY", "ALWAYS", "ACTIVATES ON", "DESCRIPTION"})
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
		t.AppendRow(table.Row{
			string(r.Source),
			r.CanonicalName(),
			string(r.ApplyMode),
			fmt.Sprintf("%v", r.AlwaysApply),
			activates,
			desc,
		})
	}
	style := table.StyleRounded
	style.Format.Header = text.FormatUpper
	t.SetStyle(style)
	t.Render()
	fmt.Printf("\n%d rule(s) under %s\n", len(rules), cwd)
	return nil
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
