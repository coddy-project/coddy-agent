package tools

import (
	"encoding/json"
)

// ToolPathsEscapeCWD reports whether a built-in tool call targets a path outside the session CWD.
// When RestrictToCWD is false, tools allow such paths; the agent must still ask the user first.
// Optional path fields that default to CWD (empty list_dir path, empty search_files path) are not outside.
func ToolPathsEscapeCWD(toolName, argsJSON, cwd string) bool {
	if cwd == "" {
		return false
	}
	switch toolName {
	case "read_file", "write_file", "write_text_file", "apply_diff":
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) != nil || a.Path == "" {
			return false
		}
		return PathEscapesCWD(ResolvePath(a.Path, cwd), cwd)
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
