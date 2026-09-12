package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// LoadInstructions reads the configured instruction files and concatenates their
// contents. Files that don't exist are silently skipped (matching other-agent
// AGENTS.md convention), as are empty ones, a file named twice, and any file
// whose path is in skip - the preamble documents the rules block already
// carries, which would otherwise reach the model a second time.
func LoadInstructions(cwd, home string, files, skip []string) string {
	seen := make(map[string]struct{}, len(files)+len(skip))
	for _, s := range skip {
		if key := instructionKey(s); key != "" {
			seen[key] = struct{}{}
		}
	}
	var parts []string
	for _, entry := range files {
		path := ResolveInstructionFile(entry, cwd, home)
		if path == "" {
			continue
		}
		key := instructionKey(path)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		body := strings.TrimSpace(string(data))
		if body == "" {
			continue
		}
		parts = append(parts, body)
	}
	return strings.Join(parts, "\n\n")
}

// instructionKey identifies a file for the dedupe, so the same bytes are not
// sent twice under two names: symlinks are resolved where the file exists
// (a CLAUDE.md pointing at AGENTS.md is one file, not two) and the case is
// folded on the platforms whose filesystems ignore it.
func instructionKey(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}
