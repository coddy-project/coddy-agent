package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The permission mode a new session starts in is not configuration: it is the
// mode the operator chose last, on any surface, for any session, kept in one
// file of the agent home (#512). A session copies it into its own metadata
// when it is created and keeps it until it is switched; switching it is a
// new choice and becomes the default of the sessions created after it.
// Without a file new sessions start in ask. The old tools.permission_mode key
// of config.yaml seeds the file once (legacy_keys.go).

// DefaultPermissionModeFile is the name of that file under the agent home.
const DefaultPermissionModeFile = "permission-mode.json"

type defaultPermissionModeDoc struct {
	PermissionMode string    `json:"permissionMode"`
	UpdatedAt      time.Time `json:"updatedAt,omitempty"`
}

// DefaultPermissionModePath is the file that keeps the default permission
// mode of new sessions; empty without a home.
func DefaultPermissionModePath(home string) string {
	if strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, DefaultPermissionModeFile)
}

// IsPermissionMode reports whether mode is one of ask, accept_edits, bypass.
func IsPermissionMode(mode string) bool {
	switch mode {
	case PermModeAsk, PermModeAcceptEdits, PermModeBypass:
		return true
	}
	return false
}

// ReadDefaultPermissionMode returns the permission mode the operator chose
// last, or "" when there is no choice on file (or the file does not read as
// one), which a caller turns into ask.
func ReadDefaultPermissionMode(home string) string {
	path := DefaultPermissionModePath(home)
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc defaultPermissionModeDoc
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	mode := strings.ToLower(strings.TrimSpace(doc.PermissionMode))
	if !IsPermissionMode(mode) {
		return ""
	}
	return mode
}

// WriteDefaultPermissionMode records mode as the one new sessions start in.
func WriteDefaultPermissionMode(home, mode string) error {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if !IsPermissionMode(mode) {
		return fmt.Errorf("unknown permission mode %q (ask, accept_edits, bypass)", mode)
	}
	path := DefaultPermissionModePath(home)
	if path == "" {
		return fmt.Errorf("no agent home to keep the default permission mode in")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(defaultPermissionModeDoc{PermissionMode: mode, UpdatedAt: time.Now().UTC()}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(raw, '\n'), 0o600)
}
