package fs

import (
	"path/filepath"
	"strings"
)

// ResolvePath returns an absolute path, resolving relative to cwd.
func ResolvePath(path, cwd string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}

// sessionStoreRoot returns the Coddy session-store root (the parent of the active
// session bundle <root>/<id>). Filesystem search tools hide this directory so that
// other sessions' transcripts can never leak into the current session's context.
// Returns "" when disk persistence is off (no SessionDir).
func sessionStoreRoot(sessionDir string) string {
	sd := strings.TrimSpace(sessionDir)
	if sd == "" {
		return ""
	}
	return filepath.Dir(filepath.Clean(sd))
}

// isWithinDir reports whether path is dir itself or located inside dir.
func isWithinDir(path, dir string) bool {
	if dir == "" || strings.TrimSpace(path) == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// grepLineFilePath extracts the file path from a ripgrep/grep "path:line:content"
// record. POSIX absolute paths contain no ':' before the line-number separator.
func grepLineFilePath(line string) string {
	idx := strings.IndexByte(line, ':')
	if idx <= 0 {
		return ""
	}
	return line[:idx]
}

// dropStoreLines removes grep result lines whose file path is inside storeRoot.
func dropStoreLines(output, storeRoot string) string {
	if storeRoot == "" {
		return output
	}
	lines := strings.Split(output, "\n")
	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		if p := grepLineFilePath(ln); p != "" && isWithinDir(p, storeRoot) {
			continue
		}
		kept = append(kept, ln)
	}
	return strings.Join(kept, "\n")
}

// dropStorePaths removes file paths located inside storeRoot.
func dropStorePaths(paths []string, storeRoot string) []string {
	if storeRoot == "" {
		return paths
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if isWithinDir(p, storeRoot) {
			continue
		}
		out = append(out, p)
	}
	return out
}

