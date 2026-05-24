package rules

import (
	"fmt"
	"os"
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
	t.AppendHeader(table.Row{"SOURCE", "NAME", "APPLY", "ALWAYS", "GLOBS", "DESCRIPTION"})
	for _, r := range rules {
		globs := strings.Join(r.Globs, ", ")
		if len(globs) > 60 {
			globs = globs[:57] + "..."
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
			globs,
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
