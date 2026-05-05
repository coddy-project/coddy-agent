package permission

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	toolfs "github.com/EvilFreelancer/coddy-agent/internal/tools/fs"
)

// WriteGrantKeys returns persisted keys for filesystem tools (toolName|absolutePath). Empty if none.
func WriteGrantKeys(toolName, argsJSON, cwd string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || strings.TrimSpace(argsJSON) == "" {
		return nil
	}
	switch toolName {
	case "write_file", "write_text_file", "apply_diff", "mkdir", "rmdir", "touch", "rm":
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) != nil || strings.TrimSpace(a.Path) == "" {
			return nil
		}
		abs, err := filepath.Abs(toolfs.ResolvePath(strings.TrimSpace(a.Path), cwd))
		if err != nil {
			return nil
		}
		return []string{toolName + "|" + abs}
	case "mv":
		var a struct {
			Src string `json:"src"`
			Dst string `json:"dst"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) != nil {
			return nil
		}
		var keys []string
		if strings.TrimSpace(a.Src) != "" {
			if abs, err := filepath.Abs(toolfs.ResolvePath(strings.TrimSpace(a.Src), cwd)); err == nil {
				keys = append(keys, "mv|"+abs)
			}
		}
		if strings.TrimSpace(a.Dst) != "" {
			if abs, err := filepath.Abs(toolfs.ResolvePath(strings.TrimSpace(a.Dst), cwd)); err == nil {
				keys = append(keys, "mv|"+abs)
			}
		}
		return keys
	default:
		return nil
	}
}

// AllWriteKeysGranted returns true when grants contains every key (non-empty keys slice).
func AllWriteKeysGranted(grants, keys []string) bool {
	if len(keys) == 0 {
		return false
	}
	for _, k := range keys {
		if !writeKeyGranted(grants, k) {
			return false
		}
	}
	return true
}

func writeKeyGranted(grants []string, key string) bool {
	for _, g := range grants {
		if g == key {
			return true
		}
	}
	return false
}

// RecordAllowAlways persists session grants when the user chose allow_always.
func RecordAllowAlways(st *session.State, toolName, argsJSON, cwd string, res *acp.PermissionResult) {
	if st == nil || res == nil || res.OptionID != "allow_always" {
		return
	}
	toolName = strings.TrimSpace(toolName)
	switch toolName {
	case "run_command":
		cmd := ExtractRunCommand(argsJSON)
		if cmd != "" {
			st.AddCommandGrantIfNew(cmd)
		}
	default:
		for _, k := range WriteGrantKeys(toolName, argsJSON, cwd) {
			st.AddWriteGrantIfNew(k)
		}
	}
}
