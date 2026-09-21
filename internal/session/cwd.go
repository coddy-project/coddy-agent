package session

import (
	"context"
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
// workspace-scoped state: the configured MCP servers are re-dialed for the new
// workspace through the trust gate while the previous workspace's are closed,
// and skills, project rules, the SessionStart hook context and the slash
// catalog follow the new cwd. The target must be an existing directory. A
// switch to another spelling of the same workspace only stores the spelling.
func (m *Manager) SetSessionWorkspace(ctx context.Context, st *State, dir string) error {
	abs, err := ValidateWorkspaceDir(dir)
	if err != nil {
		return err
	}
	prevCWD := st.GetCWD()
	st.SetCWD(abs)
	if SameWorkspacePath(prevCWD, abs) {
		return nil
	}
	m.reloadWorkspaceScopedState(ctx, st)
	return nil
}

// ValidateWorkspaceDir resolves dir to an absolute path and requires it to
// name an existing directory. It is the workspace switch's own check, exported
// so a caller can validate a target before handing it to session creation.
func ValidateWorkspaceDir(dir string) (string, error) {
	abs, err := EffectiveSessionCWD(dir, "")
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("workspace folder not found: %s", abs)
	}
	return abs, nil
}

// ReloadSessionWorkspace re-derives workspace-scoped state (configured MCP
// servers, skills, project rules, SessionStart hook context) after the
// workspace's files changed under the same cwd - an in-place branch checkout.
// The MCP re-dial is a fresh trust evaluation: a declaration the checkout
// replaced is approved again or stays cold.
func (m *Manager) ReloadSessionWorkspace(ctx context.Context, st *State) {
	m.reloadWorkspaceScopedState(ctx, st)
}

// reloadWorkspaceScopedState re-derives everything a session's workspace
// decides. The MCP re-dial goes through the same pending + prompt-turn-lock
// path a settings save uses, so it serializes against a turn in flight and a
// config reload; a turn holding the lock drains the parked reload on release.
func (m *Manager) reloadWorkspaceScopedState(ctx context.Context, st *State) {
	cwd := st.GetCWD()
	cfg := m.activeCfg()

	st.markMCPReloadPending()
	if unlock, err := m.acquirePromptTurnLock(st.GetID(), st); err == nil {
		applied := true
		if st.takeMCPReloadPending() {
			applied = m.applyConfiguredMCPReload(ctx, st)
		}
		unlock()
		if applied {
			m.drainPendingMCPReload(st.GetID(), st)
		}
	}

	loadedSkills, err := m.loadSkills(cwd, cfg)
	if err != nil {
		m.log.Warn("failed to load skills on workspace switch", "error", err)
	}
	st.ReplaceSkills(loadedSkills)
	st.ReplaceRulesCatalog(DiscoverRules(cfg, cwd))
	m.runSessionStartHooks(ctx, st, hookSourceWorkspace)
	m.sendAvailableSlashCommands(st.GetID(), st)
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
