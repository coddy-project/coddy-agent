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
	case "write", "edit", "apply_patch", "mkdir", "rmdir", "touch", "rm":
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) != nil {
			return nil
		}
		p := strings.TrimSpace(a.Path)
		if p == "" {
			return nil
		}
		abs, err := filepath.Abs(toolfs.ResolvePath(p, cwd))
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

// RecordAllowAlways persists session grants when the user chose one of the
// allow-always options. "allow_always" stores the exact command; the
// program-wide option stores the wider entry that ProgramGrant derived and that
// the dialog named on its button.
func RecordAllowAlways(st *session.State, toolName, argsJSON, cwd string, res *acp.PermissionResult) {
	if st == nil || res == nil {
		return
	}
	if res.OptionID != OptionAllowAlways && res.OptionID != OptionAllowAlwaysProgram {
		return
	}
	toolName = strings.TrimSpace(toolName)
	switch toolName {
	case "run_command":
		cmd := ExtractRunCommand(argsJSON)
		if cmd == "" {
			return
		}
		if res.OptionID == OptionAllowAlwaysProgram {
			if grant, ok := ProgramGrant(cmd); ok {
				st.AddCommandGrantIfNew(grant)
			}
			return
		}
		st.AddCommandGrantIfNew(cmd)
	default:
		// The program-wide option is only ever offered for run_command, so it
		// must not widen anything on the filesystem path.
		if res.OptionID != "allow_always" {
			return
		}
		for _, k := range WriteGrantKeys(toolName, argsJSON, cwd) {
			st.AddWriteGrantIfNew(k)
		}
	}
}

// Approved reports whether a client's answer to a permission request lets the
// tool call proceed. A cancelled request and the reject option are the only
// refusals; every other selected option is an approval, so a client that
// answers in the protocol's nested shape is honoured like Coddy's own surfaces.
func Approved(res *acp.PermissionResult) bool {
	if res == nil {
		return false
	}
	if res.Outcome == OutcomeCancelled {
		return false
	}
	return res.OptionID != OptionReject
}
