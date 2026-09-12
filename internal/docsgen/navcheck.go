package docsgen

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// StubMarker opens a redirect stub left at an old path. Stubs are exempt from
// the navigation check and from llms.txt.
const StubMarker = "<!-- docs-stub"

// generatedFiles are fully generated and never part of the map.
var generatedFiles = map[string]bool{
	"docs/README.md":     true,
	"docs/llms.txt":      true,
	"docs/llms-full.txt": true,
}

// IsStub reports whether a markdown file is a redirect stub.
func IsStub(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	if sc.Scan() {
		return strings.HasPrefix(sc.Text(), StubMarker)
	}
	return false
}

// DocsMarkdown lists every markdown file under docs/ (relative to root),
// assets excluded, stubs and generated files excluded when skipStubs is set.
func DocsMarkdown(root string, skipStubs bool) ([]string, error) {
	var out []string
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "docs/assets" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".md") {
			return nil
		}
		if skipStubs && (generatedFiles[rel] || IsStub(path)) {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out, err
}

// CheckNav verifies that the map and the tree agree: every page listed exists
// and starts with an H1, and every markdown page under docs/ is listed.
func CheckNav(root string, nav *Nav) []Problem {
	var problems []Problem
	listed := map[string]bool{}
	for _, p := range nav.Pages() {
		rel := RepoPath(p.Path)
		listed[rel] = true
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			problems = append(problems, Problem{NavFile, "page " + p.Path + " does not exist"})
			continue
		}
		if !strings.HasPrefix(string(data), "# ") {
			problems = append(problems, Problem{rel, "must start with an H1 title"})
		}
		if IsStub(filepath.Join(root, rel)) {
			problems = append(problems, Problem{NavFile, "page " + p.Path + " is a redirect stub"})
		}
	}
	files, err := DocsMarkdown(root, true)
	if err != nil {
		return append(problems, Problem{"docs", err.Error()})
	}
	for _, rel := range files {
		if !listed[rel] {
			problems = append(problems, Problem{rel, "not listed in " + NavFile})
		}
	}
	return problems
}
