package fs

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// ToolCallPaths returns the filesystem paths a built-in fs tool call targets,
// resolved against cwd. It returns nil for every other tool, including
// run_command, whose free-form shell string cannot be attributed to a
// directory reliably.
//
// Argument names are kept in sync with the *Args structs in this package.
func ToolCallPaths(toolName, argsJSON, cwd string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || strings.TrimSpace(argsJSON) == "" {
		return nil
	}
	switch toolName {
	case "read", "glob", "grep", "print_tree", "edit", "write", "apply_patch",
		"mkdir", "rmdir", "touch", "rm":
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) != nil {
			return nil
		}
		// glob and grep declare path as optional; an empty value is skipped
		// rather than defaulted to cwd.
		return absPaths(cwd, a.Path)
	case "mv":
		var a struct {
			Src string `json:"src"`
			Dst string `json:"dst"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) != nil {
			return nil
		}
		return absPaths(cwd, a.Src, a.Dst)
	default:
		return nil
	}
}

// absPaths resolves non-empty paths against cwd and makes them absolute.
func absPaths(cwd string, paths ...string) []string {
	var out []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(ResolvePath(p, cwd))
		if err != nil {
			continue
		}
		out = append(out, abs)
	}
	return out
}
