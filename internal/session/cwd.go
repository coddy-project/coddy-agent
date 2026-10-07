package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
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
	return m.setSessionWorkspace(ctx, st, dir, false)
}

// SetSessionWorkspaceDuringTurn moves a session whose prompt turn lock is
// already held by the caller. Its configured MCP clients are refreshed under
// that lock before the next model step, so old project servers are not offered
// in the new workspace.
func (m *Manager) SetSessionWorkspaceDuringTurn(ctx context.Context, st *State, dir string) error {
	return m.setSessionWorkspace(ctx, st, dir, true)
}

func (m *Manager) setSessionWorkspace(ctx context.Context, st *State, dir string, turnOwned bool) error {
	abs, err := ValidateWorkspaceDir(dir)
	if err != nil {
		return err
	}
	prevCWD := st.GetCWD()
	st.SetCWD(abs)
	if SameWorkspacePath(prevCWD, abs) {
		return nil
	}
	m.reloadWorkspaceScopedState(ctx, st, turnOwned)
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

// RecoverManagedWorktreeCWD returns the repository root for a missing path
// below a managed <repo>/.coddy/worktrees/<name> directory. It never recovers
// arbitrary missing directories: the repository root must exist and be the
// main checkout of a Git repository.
func RecoverManagedWorktreeCWD(dir string) (string, bool) {
	abs, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil {
		return "", false
	}
	abs = filepath.Clean(abs)
	if info, err := os.Stat(abs); err == nil && info.IsDir() {
		return "", false
	}

	for child, parent := abs, filepath.Dir(abs); child != parent; child, parent = parent, filepath.Dir(parent) {
		if filepath.Base(parent) != "worktrees" || filepath.Base(filepath.Dir(parent)) != ".coddy" {
			continue
		}
		repo := filepath.Dir(filepath.Dir(parent))
		info, err := os.Stat(repo)
		if err != nil || !info.IsDir() {
			return "", false
		}
		if CanonicalWorkspacePath(gitws.MainCheckoutRoot(repo)) != CanonicalWorkspacePath(repo) {
			return "", false
		}
		return repo, true
	}
	return "", false
}

// ReloadSessionWorkspace re-derives workspace-scoped state (configured MCP
// servers, skills, project rules, SessionStart hook context) after the
// workspace's files changed under the same cwd - an in-place branch checkout.
// The MCP re-dial is a fresh trust evaluation: a declaration the checkout
// replaced is approved again or stays cold.
func (m *Manager) ReloadSessionWorkspace(ctx context.Context, st *State) {
	m.reloadWorkspaceScopedState(ctx, st, false)
}

// reloadWorkspaceScopedState re-derives everything a session's workspace
// decides. External switches acquire the turn lock or park the MCP reload;
// a worktree_create call already owns that lock and refreshes its MCP clients
// before the next model step.
func (m *Manager) reloadWorkspaceScopedState(ctx context.Context, st *State, turnOwned bool) {
	cwd := st.GetCWD()
	cfg := m.activeCfg()

	if turnOwned {
		dialCtx, cancel := context.WithTimeout(ctx, mcpReloadTimeout)
		if !m.applyConfiguredMCPReload(dialCtx, st) {
			m.dropStaleConfiguredMCPClients(st)
		}
		cancel()
	} else {
		// A directory selection must make local skills available to the
		// composer immediately, but it is not an agent turn and must not
		// execute a workspace-provided MCP process. Reconcile configured MCP
		// servers under the next turn lock instead, where trust is evaluated.
		st.markMCPReloadPending()
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
