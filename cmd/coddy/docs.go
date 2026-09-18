package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/docs"
)

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
	usage := fmt.Errorf("usage: %s docs [list] | search <words> [--limit N] | show <page>[#section]", os.Args[0])
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
