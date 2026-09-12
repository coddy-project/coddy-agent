package session

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EffectiveSessionCWD resolves the filesystem working directory for a new session.
// If clientCWD is empty or whitespace, defaultCWD is used. Result is absolute.
func EffectiveSessionCWD(clientCWD, defaultCWD string) (string, error) {
	s := strings.TrimSpace(clientCWD)
	if s == "" {
		s = strings.TrimSpace(defaultCWD)
	}
	if s == "" {
		return "", fmt.Errorf("session cwd is empty")
	}
	abs, err := filepath.Abs(s)
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	return abs, nil
}

// SetSessionWorkspace switches the session working directory and re-derives
// cwd-scoped state (skills, project rules, slash commands). The target must
// be an existing directory.
func (m *Manager) SetSessionWorkspace(st *State, dir string) error {
	abs, err := EffectiveSessionCWD(dir, "")
	if err != nil {
		return err
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("workspace folder not found: %s", abs)
	}
	st.SetCWD(abs)

	cfg := m.activeCfg()
	loadedSkills, err := m.loadSkills(abs, cfg)
	if err != nil {
		m.log.Warn("failed to load skills on workspace switch", "error", err)
	}
	st.ReplaceSkills(loadedSkills)
	st.ReplaceRulesCatalog(DiscoverRules(cfg, abs))
	m.sendAvailableSlashCommands(st.GetID(), st)
	return nil
}

// caseInsensitivePaths marks the platforms whose default filesystems fold
// case, so two spellings of a folder that differ only in case are one folder.
var caseInsensitivePaths = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// CanonicalWorkspacePath returns the form two spellings of one folder share:
// absolute, cleaned, with symlinks resolved when the folder exists. Sessions
// keep the cwd as the client gave it (the console stores the logical $PWD of
// a symlinked checkout, an editor sends the physical path, a Windows client
// may differ in the drive letter's case), so every workspace filter compares
// this form rather than the stored string.
func CanonicalWorkspacePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = filepath.Clean(p)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

// SameWorkspacePath reports whether two paths name the same folder.
func SameWorkspacePath(a, b string) bool {
	return matchesWorkspace(CanonicalWorkspacePath(a), b)
}

// matchesWorkspace compares an already canonical filter with a stored path.
func matchesWorkspace(canonical, stored string) bool {
	c := CanonicalWorkspacePath(stored)
	if c == canonical {
		return true
	}
	return caseInsensitivePaths && strings.EqualFold(c, canonical)
}
