package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// projectDirVars are the variables the runner sets to the workspace, as a
// command line may name a script through them.
var projectDirVars = []string{"CODDY_PROJECT_DIR", "CLAUDE_PROJECT_DIR"}

// digestWithScripts is the digest a receipt of a project hooks file binds:
// the file's own bytes and, when its commands name files of the workspace -
// a script run directly or handed to an interpreter - the content of each.
// Editing such a script withdraws the approval, as editing the file does,
// and so does a named script that appears after the approval. A file whose
// commands name none keeps the digest of its bytes alone, so receipts
// written before stay valid.
func digestWithScripts(data []byte, def *Definition, cwd string) string {
	sum := sha256.Sum256(data)
	plain := "sha256:" + hex.EncodeToString(sum[:])
	workspace := lexicalPath(cwd)
	if def == nil || workspace == "" {
		return plain
	}
	scripts := map[string]string{}
	for _, groups := range def.Events {
		for _, g := range groups {
			for _, h := range g.Handlers {
				for _, tok := range handlerTokens(h) {
					rel, state := scriptState(tok, workspace)
					if rel != "" {
						scripts[rel] = state
					}
				}
			}
		}
	}
	if len(scripts) == 0 {
		return plain
	}
	names := make([]string, 0, len(scripts))
	for rel := range scripts {
		names = append(names, rel)
	}
	sort.Strings(names)
	hash := sha256.New()
	hash.Write(data)
	for _, rel := range names {
		hash.Write([]byte("\x00script\x00" + rel + "\x00" + scripts[rel]))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

// handlerTokens are the words of a handler's command lines that may name a
// file it runs: the shell form through shellWords, the exec form's executable
// and arguments as given.
func handlerTokens(h Handler) []string {
	var out []string
	for _, line := range []string{h.Command, h.CommandWindows} {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if h.ExecForm {
			out = append(out, line)
			continue
		}
		out = append(out, shellWords(line)...)
	}
	if h.ExecForm {
		out = append(out, h.Args...)
	}
	return out
}

// shellSpelling joins a word where quotes do, as in
// `"$CLAUDE_PROJECT_DIR"/hooks/x.sh`, and splits one where the shell or a
// `--name=value` option does. An output redirection stays a word of its own.
var shellSpelling = strings.NewReplacer(
	`"`, "", `'`, "",
	";", " ", "&", " ", "|", " ", "(", " ", ")", " ", "`", " ", "<", " ", "=", " ",
	">", " > ",
)

// shellWords are the words of a shell line, without the one after an output
// redirection: a file the command writes is no script it runs, and a log that
// grows with every run must not withdraw the approval after every run.
func shellWords(line string) []string {
	var out []string
	output := false
	for _, word := range strings.Fields(shellSpelling.Replace(line)) {
		switch {
		case word == ">":
			output = true
		case output:
			output = false
		default:
			out = append(out, word)
		}
	}
	return out
}

// scriptState names a token that is a file of the workspace by its
// workspace-relative path and gives the digest of its content, or "missing"
// for a path-like token that names nothing yet. Anything else - an option, a
// program on PATH, a file outside the workspace - gives "".
func scriptState(tok, workspace string) (rel, state string) {
	tok = strings.TrimSpace(tok)
	if tok == "" || strings.HasPrefix(tok, "-") {
		return "", ""
	}
	for _, v := range projectDirVars {
		tok = strings.ReplaceAll(tok, "${"+v+"}", workspace)
		tok = strings.ReplaceAll(tok, "$"+v, workspace)
	}
	if strings.Contains(tok, "$") {
		return "", ""
	}
	pathLike := strings.ContainsAny(tok, `/\`)
	abs := tok
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workspace, abs)
	}
	abs = filepath.Clean(abs)
	r, err := filepath.Rel(workspace, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", ""
	}
	rel = filepath.ToSlash(r)
	st, err := os.Stat(abs)
	if err != nil {
		if pathLike && os.IsNotExist(err) {
			return rel, "missing"
		}
		return "", ""
	}
	if !st.Mode().IsRegular() {
		return "", ""
	}
	// A link inside the workspace that leads out is not the workspace's file.
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		if !under(CanonicalWorkspace(real), CanonicalWorkspace(workspace)) {
			return "", ""
		}
	}
	f, err := os.Open(abs) // a file of the workspace, named by a hook the operator is asked to approve
	if err != nil {
		return rel, "unreadable"
	}
	defer func() { _ = f.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return rel, "unreadable"
	}
	return rel, hex.EncodeToString(hash.Sum(nil))
}
