package docsgen

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// generatedFiles are fully generated and never part of the map.
var generatedFiles = map[string]bool{
	"docs/README.md":     true,
	"docs/llms.txt":      true,
	"docs/llms-full.txt": true,
}

// internalDir holds the design records: ours, not a reader's, so they stay
// out of the map and therefore out of the hub, llms.txt and llms-full.txt.
// Their own links are still checked, because a page they point at can move.
const internalDir = "docs/plans/"

// DocsMarkdown lists every markdown file under docs/ (relative to root),
// assets excluded, the generated files excluded when skipGenerated is set.
func DocsMarkdown(root string, skipGenerated bool) ([]string, error) {
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
		if skipGenerated && generatedFiles[rel] {
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
	}
	files, err := DocsMarkdown(root, true)
	if err != nil {
		return append(problems, Problem{"docs", err.Error()})
	}
	for _, rel := range files {
		if !listed[rel] && !strings.HasPrefix(rel, internalDir) {
			problems = append(problems, Problem{rel, "not listed in " + NavFile})
		}
	}
	return problems
}
