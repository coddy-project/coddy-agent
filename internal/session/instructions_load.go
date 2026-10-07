package session

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/rules"
)

// ResolveInstructionFile resolves one instructions.files entry to an absolute
// path. ${CODDY_HOME} and ${CWD} expand, a leading ~ expands to the user's home
// directory, an absolute entry is taken as it stands, and a relative one is
// anchored at the session workspace. An entry that cannot be resolved - a
// ${CODDY_HOME} reference without a home, a relative entry without a cwd -
// returns "" rather than a path assembled out of a literal placeholder.
func ResolveInstructionFile(entry, cwd, home string) string {
	path := strings.TrimSpace(entry)
	if path == "" {
		return ""
	}
	if strings.Contains(path, "${CODDY_HOME}") {
		if strings.TrimSpace(home) == "" {
			return ""
		}
		path = strings.ReplaceAll(path, "${CODDY_HOME}", home)
	}
	if strings.Contains(path, "${CWD}") {
		if strings.TrimSpace(cwd) == "" {
			return ""
		}
		path = strings.ReplaceAll(path, "${CWD}", cwd)
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		path = filepath.Join(userHome, strings.TrimLeft(strings.TrimPrefix(path, "~"), `/\`))
	}
	if !filepath.IsAbs(path) {
		if strings.TrimSpace(cwd) == "" {
			return ""
		}
		path = filepath.Join(cwd, path)
	}
	return filepath.Clean(path)
}

// ResolveInstructionFiles resolves the entries of instructions.files, in their
// order, for rules.LoadStanding: the files the operator added below the
// AGENTS.md and DESIGN.md layers. An entry that cannot be resolved is left
// out.
func ResolveInstructionFiles(entries []string, cwd, home string) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if path := ResolveInstructionFile(entry, cwd, home); path != "" {
			out = append(out, path)
		}
	}
	return out
}

// UnreadInstruction is an entry of instructions.files whose file a session
// does not read, for the surfaces that tell the operator so: the log, the
// console header, --dry-run.
type UnreadInstruction struct {
	// Index is the entry's position in instructions.files.
	Index int
	// Entry is the entry as written.
	Entry string
	// Path is the file it resolves to.
	Path string
	// Err says why the file is not read (rules.CheckDoc).
	Err error
}

// Reason is Err in a few words, to follow the file's name.
func (u UnreadInstruction) Reason() string {
	return UnreadReason(u.Err)
}

// UnreadReason words why a document is not read, to follow its name: "does
// not exist", "cannot be read: permission denied", "is a folder, not a file",
// "is empty".
func UnreadReason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, fs.ErrNotExist):
		return "does not exist"
	case errors.Is(err, fs.ErrPermission):
		return "cannot be read: permission denied"
	case errors.Is(err, rules.ErrDocIsFolder), errors.Is(err, rules.ErrDocEmpty):
		return err.Error()
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return "cannot be read: " + pathErr.Err.Error()
	}
	return "cannot be read: " + err.Error()
}

// CheckInstructionFile resolves one instructions.files entry for a session in
// cwd and reports why its file would not be read (nil when it would), and
// whether the entry names a file of the workspace - relative or under ${CWD} -
// rather than the same file in every workspace (an absolute path, ~,
// ${CODDY_HOME}). path is "" for an entry that does not resolve.
func CheckInstructionFile(entry, cwd, home string) (path string, workspace bool, err error) {
	path = ResolveInstructionFile(entry, cwd, home)
	if path == "" {
		return "", false, nil
	}
	// Without a workspace, an entry that still resolves names the same file
	// wherever the session runs.
	workspace = ResolveInstructionFile(entry, "", home) == ""
	return path, workspace, rules.CheckDoc(path)
}

// UnreadInstructionFiles reports the entries of instructions.files whose file
// a session in cwd does not read. LoadStanding skips such a file without a
// word - a list may name files that only some workspaces carry - so this is
// where an entry naming the same file in every workspace, the way several
// agents with configurations of their own share one set of instructions,
// shows that it points at nothing: a path the process does not see (a folder
// not mounted into its container), a file it may not read, a folder, an empty
// file. A workspace entry is reported only when its file is there and cannot
// be read, never for being absent from this workspace.
func UnreadInstructionFiles(entries []string, cwd, home string) []UnreadInstruction {
	var out []UnreadInstruction
	for i, entry := range entries {
		path, workspace, err := CheckInstructionFile(entry, cwd, home)
		if path == "" || err == nil || (workspace && errors.Is(err, fs.ErrNotExist)) {
			continue
		}
		out = append(out, UnreadInstruction{Index: i, Entry: entry, Path: path, Err: err})
	}
	return out
}
