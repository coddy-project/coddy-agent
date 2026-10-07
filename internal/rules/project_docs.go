package rules

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const projectDocMaxBytes = 256 * 1024

// ErrDocIsFolder reports a document path that names a folder.
var ErrDocIsFolder = errors.New("is a folder, not a file")

// ErrDocEmpty reports a document that holds nothing but white space: it is
// there and adds nothing to the prompt.
var ErrDocEmpty = errors.New("is empty")

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

// Standing is what the system prompt of a session carries for one rules
// generation besides the rules: the AGENTS.md and DESIGN.md of the agent home
// and of the session folder (Docs, the first two layers: the operator's pair
// first, so what the person running coddy wants is read before what the
// checkout says about itself; nothing configures either pair, the file being
// there is the switch), then the files the
// operator listed in instructions.files (User), and the key (DocKey) of every
// file that entered one of the two (Keys). A document already in the prompt is
// not sent again: a later layer, a repeated entry of the list, or a nested
// document a tool call or a mention brings in (the third layer) that Keys
// already holds is left out.
type Standing struct {
	Docs []ProjectDoc
	User []ProjectDoc
	Keys map[string]bool
}

// LoadStanding reads the first two layers and then the user files, in that
// order, each file at most once: the first occurrence wins, since its text is
// already in the prompt above anything that would repeat it. userPaths are the
// absolute paths of the instructions.files entries, resolved by the caller;
// "" is skipped. A file that is absent or holds nothing enters nothing and
// takes no key, so it is still read when it appears later in the session.
func LoadStanding(home, cwd string, userPaths []string) Standing {
	st := Standing{Keys: map[string]bool{}}
	take := func(path string) (ProjectDoc, bool) {
		key := DocKey(path)
		if key == "" || st.Keys[key] {
			return ProjectDoc{}, false
		}
		doc, err := loadProjectDoc(path)
		if err != nil {
			return ProjectDoc{}, false
		}
		st.Keys[key] = true
		return doc, true
	}
	if strings.TrimSpace(home) != "" {
		for _, file := range preambleFiles {
			p := filepath.Join(home, file)
			if doc, ok := take(p); ok {
				doc.Label = homeDocLabel(p)
				st.Docs = append(st.Docs, doc)
			}
		}
	}
	if strings.TrimSpace(cwd) != "" {
		for _, file := range preambleFiles {
			if doc, ok := take(filepath.Join(cwd, file)); ok {
				doc.Label = file
				st.Docs = append(st.Docs, doc)
			}
		}
	}
	for _, p := range userPaths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if doc, ok := take(p); ok {
			doc.Label = UserDocLabel(cwd, p)
			st.User = append(st.User, doc)
		}
	}
	return st
}

// RenderDocs renders the preamble documents as the sections of the rules
// block: "### <label>" and the document, one after the other.
func RenderDocs(docs []ProjectDoc) string {
	parts := make([]string, 0, len(docs))
	for _, d := range docs {
		parts = append(parts, "### "+d.Label+"\n\n"+d.Content)
	}
	return strings.Join(parts, "\n\n")
}

// RenderUserDocs renders the files of instructions.files the way RenderDocs
// renders the documents: each under a "### <label>" heading that names it.
// Without the name the model holds the text but cannot tell it is the file
// the operator talks about, and asked to follow infrastructure.md it reads a
// file it already has.
func RenderUserDocs(docs []ProjectDoc) string {
	return RenderDocs(docs)
}

// CheckDoc reports why the document at path would not reach the prompt - it
// does not exist, cannot be read, names a folder (ErrDocIsFolder) or holds
// nothing (ErrDocEmpty) - and nil when it would.
func CheckDoc(path string) error {
	_, err := loadProjectDoc(path)
	return err
}

// loadProjectDoc reads one preamble document, cut at projectDocMaxBytes, or
// says why it has nothing to give.
func loadProjectDoc(path string) (ProjectDoc, error) {
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		return ProjectDoc{}, ErrDocIsFolder
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ProjectDoc{}, err
	}
	content := strings.TrimSpace(string(b))
	if content == "" {
		return ProjectDoc{}, ErrDocEmpty
	}
	if len(content) > projectDocMaxBytes {
		content = content[:projectDocMaxBytes] + "\n\n...(truncated)"
	}
	return ProjectDoc{Path: path, Content: content}, nil
}

// UserDocLabel names a file of instructions.files for a reader: relative to
// the session folder when it lives there, else the way homeDocLabel names it.
// It heads the file's text in the prompt (RenderUserDocs) and names it on the
// surfaces that list what a session reads (the console header).
func UserDocLabel(cwd, path string) string {
	if strings.TrimSpace(cwd) != "" {
		if rel, err := filepath.Rel(cwd, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return filepath.ToSlash(rel)
		}
	}
	return homeDocLabel(path)
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
