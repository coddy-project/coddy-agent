package rules

import (
	"path/filepath"
	"runtime"
	"strings"
)

// DocKey identifies a document for the dedupe of the prompt, so the same file
// never reaches the model twice under two names: its real path, symlinks
// resolved where the file exists (a project AGENTS.md that links to the agent
// home's is one file, not two), cleaned, and folded to lower case on the
// platforms whose filesystems ignore case by default (Windows, macOS). The
// folding is by platform, not by volume: a case-sensitive volume on a Mac
// could merge two names that differ only in case.
func DocKey(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(path)
	}
	return path
}
