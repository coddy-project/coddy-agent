package session

import (
	"fmt"
	"os"
	"path/filepath"
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
	loadedSkills, err := m.skillsLoad.LoadAll(abs, cfg.Paths.Home, cfg.Skills.ManagedDir(cfg.Paths.Home))
	if err != nil {
		m.log.Warn("failed to load skills on workspace switch", "error", err)
	}
	st.ReplaceSkills(loadedSkills)
	st.ReplaceRulesCatalog(DiscoverRules(cfg, abs))
	m.sendAvailableSlashCommands(st.GetID(), st)
	return nil
}
