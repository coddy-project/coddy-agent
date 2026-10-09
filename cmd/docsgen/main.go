// Command docsgen generates the parts of the documentation that follow the code
// or the navigation map and checks the rest. See docs/contributing/documentation.md.
//
//	go run ./cmd/docsgen -write            # regenerate (make docs)
//	go run ./cmd/docsgen                   # check only, exit 1 on drift (make docs-check)
//	go run ./cmd/docsgen -changelog -write # also refresh the changelog from GitHub Releases
//	go run ./cmd/docsgen -stamp docs/ru/features/mcp.md  # record that a translation is current (make docs-stamp)
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EvilFreelancer/coddy-agent/internal/docsgen"
)

func main() {
	root := flag.String("root", ".", "repository root")
	write := flag.Bool("write", false, "write the generated files instead of checking them")
	tags := flag.String("tags", "", "build tags of the coddy binary the CLI reference is generated from")
	binary := flag.String("coddy", "", "path to a built coddy binary (built from source when empty)")
	skipCLI := flag.Bool("skip-cli", false, "leave the CLI reference untouched (no binary is built or run)")
	siteDir := flag.String("site", "", "checkout of coddy-project.github.io: render its docs layer (redirect pages, Markdown twins, llms files)")
	siteOnly := flag.Bool("site-only", false, "with -site: touch only the site checkout, leave the repository files alone")
	rawBase := flag.String("raw-base", docsgen.DefaultRawBase, "base URL of the raw Markdown for llms.txt")
	changelog := flag.Bool("changelog", false, "refresh the changelog from GitHub Releases (needs gh and network)")
	repo := flag.String("repo", "coddy-project/coddy-agent", "GitHub repository for the changelog")
	stamp := flag.Bool("stamp", false, "stamp the translated pages named as arguments: they follow their English pages as they are now")
	unchanged := flag.Bool("unchanged", false, "with -stamp: accept a translation that did not change since HEAD (the English change needs none)")
	staleTranslations := flag.String("stale-translations", "error", "a translation behind its English page is an error or a warning (the pre-commit hook)")
	flag.Parse()

	abs, err := filepath.Abs(*root)
	if err != nil {
		fail(err)
	}
	if *stamp {
		if flag.NArg() == 0 {
			fail(fmt.Errorf("-stamp needs the translated pages to stamp (docs/ru/...)"))
		}
		report, err := docsgen.Stamp(abs, flag.Args(), *unchanged)
		for _, line := range report {
			fmt.Println(line)
		}
		if err != nil {
			fail(err)
		}
		return
	}
	if *changelog {
		releases, err := docsgen.FetchReleases(*repo)
		if err != nil {
			fail(err)
		}
		if *write {
			p := filepath.Join(abs, docsgen.ChangelogFile)
			if err := os.WriteFile(p, []byte(docsgen.Changelog(*repo, releases)), 0o644); err != nil {
				fail(err)
			}
			fmt.Printf("wrote %s (%d releases)\n", docsgen.ChangelogFile, len(releases))
		}
	}
	site := ""
	if *siteDir != "" {
		if site, err = filepath.Abs(*siteDir); err != nil {
			fail(err)
		}
	}
	res, err := docsgen.Generate(docsgen.Options{Root: abs, Binary: *binary, Tags: *tags, RawBase: *rawBase, SkipCLI: *skipCLI, SiteDir: site, StaleTranslationsWarn: *staleTranslations == "warn"})
	if err != nil {
		fail(err)
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	if *write {
		if !*siteOnly {
			if err := res.Write(abs); err != nil {
				fail(err)
			}
			for rel := range res.Files {
				fmt.Println("wrote", rel)
			}
		}
		if site != "" {
			if err := docsgen.WriteSite(site, res.SiteFiles); err != nil {
				fail(err)
			}
			fmt.Printf("wrote %d site files under %s\n", len(res.SiteFiles), site)
		}
	} else {
		if !*siteOnly {
			res.Problems = append(res.Problems, res.Stale(abs)...)
		}
		if site != "" {
			res.Problems = append(res.Problems, docsgen.SiteStale(site, res.SiteFiles)...)
		}
	}
	for _, p := range res.Problems {
		fmt.Fprintln(os.Stderr, p)
	}
	if len(res.Problems) > 0 {
		fmt.Fprintf(os.Stderr, "docsgen: %d problem(s)\n", len(res.Problems))
		os.Exit(1)
	}
	fmt.Println("docsgen: ok")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "docsgen:", err)
	os.Exit(2)
}
