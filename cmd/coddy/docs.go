package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/docs"
)

// docsVerbs are the verbs `coddy docs` accepts, in the order the usage line
// names them. The switch in runDocs is the authority; this list mirrors it so
// the usage line and the tools that teach the command to the model cannot
// drift from it.
var docsVerbs = []string{"list", "search", "show"}

// docsUsage is the one usage line of `coddy docs`, spelled from the verb list so
// the two cannot drift.
func docsUsage() string {
	return fmt.Sprintf("usage: %s docs [%s] | %s <words> [--limit N] | %s <page>[#section]",
		os.Args[0], docsVerbList("list"), docsVerbList("search"), docsVerbList("show"))
}

// docsVerbList returns want when docsVerbs still has it, and otherwise a spelling
// that stands out in the usage line and fails the test that reads it back.
func docsVerbList(want string) string {
	for _, v := range docsVerbs {
		if v == want {
			return v
		}
	}
	return "?" + want
}

// runDocs prints the documentation built into this binary: its contents, a
// search, or a page or one section of it. It works in every build, the lean
// one without the console included, and reads nothing but the binary.
func runDocs(args []string, out io.Writer) error {
	lib, err := docs.Default()
	if err != nil {
		return err
	}
	verb := "list"
	if len(args) > 0 {
		verb, args = args[0], args[1:]
	}
	usage := fmt.Errorf("%s", docsUsage())
	switch verb {
	case "list":
		if len(args) > 0 && args[0] == "--slugs" {
			// One slug per line, for shell completion.
			for _, p := range lib.Pages() {
				_, _ = fmt.Fprintln(out, p.Slug)
			}
			return nil
		}
		_, _ = fmt.Fprintf(out, "Coddy %s documentation. Read a page with: %s docs show <page>\n\n", lib.Version, os.Args[0])
		_, _ = io.WriteString(out, lib.Contents())
		return nil
	case "search":
		fs := flag.NewFlagSet("docs search", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		limit := fs.Int("limit", 10, "sections to print")
		var words []string
		// Flags may come before, between or after the words.
		for len(args) > 0 {
			if err := fs.Parse(args); err != nil {
				return usage
			}
			if fs.NArg() == 0 {
				break
			}
			words = append(words, fs.Arg(0))
			args = fs.Args()[1:]
		}
		query := strings.Join(words, " ")
		if strings.TrimSpace(query) == "" {
			return usage
		}
		hits := lib.Search(query, max(*limit, 1))
		if len(hits) == 0 {
			return fmt.Errorf("no section of the documentation matches %q", query)
		}
		_, _ = io.WriteString(out, docs.FormatHits(hits))
		return nil
	case "show":
		if len(args) != 1 {
			return usage
		}
		page, anchor, err := lib.Resolve(args[0])
		if err != nil {
			return err
		}
		text := page.Markdown
		if anchor != "" {
			_, text, _ = page.Section(anchor)
		}
		_, _ = io.WriteString(out, strings.TrimRight(text, "\n")+"\n")
		return nil
	}
	return usage
}
