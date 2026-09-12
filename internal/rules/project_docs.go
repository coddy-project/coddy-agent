package rules

import (
	"os"
	"path/filepath"
	"strings"
)

const projectDocMaxBytes = 256 * 1024

// ProjectDoc holds preamble content for an AGENTS.md or a DESIGN.md.
type ProjectDoc struct {
	Label   string
	Path    string
	Content string
}

// preambleFiles are the documents a session always carries, in the order they
// reach the prompt: what a directory says about itself, AGENTS.md before
// DESIGN.md.
var preambleFiles = []string{"AGENTS.md", "DESIGN.md"}

// LoadProjectDocs reads the preamble documents of two directories: the agent
// home first, so what the person running coddy wants is read before what the
// checkout says about itself, then the session workspace. Whichever of the two
// files is there is read; nothing configures either pair - the file being
// there is the switch.
func LoadProjectDocs(home, cwd string) []ProjectDoc {
	var out []ProjectDoc
	if strings.TrimSpace(home) != "" {
		for _, file := range preambleFiles {
			p := filepath.Join(home, file)
			if doc, ok := readProjectDoc(p, homeDocLabel(p)); ok {
				out = append(out, doc)
			}
		}
	}
	for _, file := range preambleFiles {
		p := filepath.Join(cwd, file)
		if doc, ok := readProjectDoc(p, file); ok {
			out = append(out, doc)
		}
	}
	return out
}

// readProjectDoc reads one preamble document, reporting false for a file that
// is absent or holds nothing.
func readProjectDoc(path, label string) (ProjectDoc, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ProjectDoc{}, false
	}
	content := strings.TrimSpace(string(b))
	if content == "" {
		return ProjectDoc{}, false
	}
	if len(content) > projectDocMaxBytes {
		content = content[:projectDocMaxBytes] + "\n\n...(truncated)"
	}
	return ProjectDoc{Label: label, Path: path, Content: content}, true
}

// homeDocLabel names the operator's file the way they would write it, with the
// user's home directory collapsed so a real account name stays out of every
// request. An agent home outside it is named in full.
func homeDocLabel(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(path)
}
