package config

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
)

// dropWorkspaceLoosening undoes what a workspace's config.yaml may not decide.
// That file is read only because the Coddy home has none and no file was
// named, so it may have come with a checkout (legacy_keys.go treats it the
// same way). The trust policies exist to keep a checkout's own hooks, MCP
// servers, subagents, skill sources and scheduler jobs from running without
// the operator's approval, and the command allowlist lets commands run
// without a question: a file inside the checkout cannot widen either. A
// policy of deny only narrows and is kept. The operator's own file, or one
// named with --config, is never touched.
func dropWorkspaceLoosening(cfg *Config) {
	if cfg == nil || !cfg.Paths.ConfigFromWorkspace {
		return
	}
	for _, p := range looseningPolicies(cfg) {
		if *p.value == ProjectTrustAllow {
			*p.value = p.ask
			warnWorkspaceLoosening(cfg, p.key)
		}
	}
	if len(cfg.Tools.CommandAllowlist) > 0 {
		cfg.Tools.CommandAllowlist = nil
		warnWorkspaceLoosening(cfg, "tools.command_allowlist")
	}
}

// RefuseWorkspaceLoosening is the save-time side of dropWorkspaceLoosening:
// a configuration about to be written to the workspace's config.yaml that
// sets what the load would drop is refused with the keys named, so a save
// from the settings screen does not write a value the next load ignores.
func RefuseWorkspaceLoosening(cfg *Config) error {
	if cfg == nil || !cfg.Paths.ConfigFromWorkspace {
		return nil
	}
	var keys []string
	for _, p := range looseningPolicies(cfg) {
		if *p.value == ProjectTrustAllow {
			keys = append(keys, p.key)
		}
	}
	if len(cfg.Tools.CommandAllowlist) > 0 {
		keys = append(keys, "tools.command_allowlist")
	}
	if len(keys) == 0 {
		return nil
	}
	return fmt.Errorf("%s: the workspace's config.yaml, read because the Coddy home has none, cannot widen what a checkout may run; put it in %s or start with --config", strings.Join(keys, ", "), filepath.Join(cfg.Paths.Home, "config.yaml"))
}

type looseningPolicy struct {
	key   string
	value *string
	ask   string
}

// looseningPolicies are the trust policies of cfg an allow in the
// workspace's file may not set, with the value each falls back to.
func looseningPolicies(cfg *Config) []looseningPolicy {
	return []looseningPolicy{
		{"mcp.project_trust", &cfg.MCP.ProjectTrust, ProjectTrustAsk},
		{"hooks.project_trust", &cfg.Hooks.ProjectTrust, ProjectTrustAsk},
		{"subagents.project_trust", &cfg.Subagents.ProjectTrust, SubagentsProjectTrustAsk},
		{"skills.project_trust", &cfg.Skills.ProjectTrust, ProjectTrustAsk},
		{"scheduler.project_trust", &cfg.Scheduler.ProjectTrust, ProjectTrustAsk},
	}
}

func warnWorkspaceLoosening(cfg *Config, key string) {
	slog.Default().Warn("config: the workspace's config.yaml, read because the Coddy home has none, cannot widen what a checkout may run; the key is not used (put it in the home's config.yaml or name the file with --config)", "key", key, "config", cfg.Paths.ConfigPath, "home", cfg.Paths.Home)
}
