package fs

import (
	"encoding/json"
	"fmt"
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

// CheckInsideCWD returns an error if path escapes the cwd.
func CheckInsideCWD(path, cwd string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(abs, cwdAbs+string(filepath.Separator)) && abs != cwdAbs {
		return fmt.Errorf("path %s is outside working directory %s", path, cwd)
	}
	return nil
}

// PathEscapesCWD reports whether path resolves outside cwd (not under cwd).
func PathEscapesCWD(path, cwd string) bool {
	return CheckInsideCWD(path, cwd) != nil
}

// ToolPathsEscapeCWD reports whether a built-in tool call targets a path outside the session CWD.
// When RestrictToCWD is false, tools allow such paths; the agent must still ask the user first.
// Optional path fields that default to CWD (empty list_dir path, empty search_files path) are not outside.
func ToolPathsEscapeCWD(toolName, argsJSON, cwd string) bool {
	if cwd == "" {
		return false
	}
	switch toolName {
	case "read_file", "write_file", "write_text_file", "apply_diff", "mkdir", "rmdir", "touch", "rm":
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) != nil || a.Path == "" {
			return false
		}
		return PathEscapesCWD(ResolvePath(a.Path, cwd), cwd)
	case "mv":
		var a struct {
			Src string `json:"src"`
			Dst string `json:"dst"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) != nil {
			return false
		}
		if a.Src != "" && PathEscapesCWD(ResolvePath(a.Src, cwd), cwd) {
			return true
		}
		if a.Dst != "" && PathEscapesCWD(ResolvePath(a.Dst, cwd), cwd) {
			return true
		}
		return false
	case "list_dir":
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) != nil {
			return false
		}
		dirPath := cwd
		if a.Path != "" {
			dirPath = ResolvePath(a.Path, cwd)
		}
		return PathEscapesCWD(dirPath, cwd)
	case "search_files":
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) != nil || a.Path == "" {
			return false
		}
		return PathEscapesCWD(ResolvePath(a.Path, cwd), cwd)
	default:
		return false
	}
}
