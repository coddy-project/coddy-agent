package config

import "log/slog"

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
	var dropped []string
	for _, p := range []struct {
		key   string
		value *string
		ask   string
	}{
		{"mcp.project_trust", &cfg.MCP.ProjectTrust, ProjectTrustAsk},
		{"hooks.project_trust", &cfg.Hooks.ProjectTrust, ProjectTrustAsk},
		{"subagents.project_trust", &cfg.Subagents.ProjectTrust, SubagentsProjectTrustAsk},
		{"skills.project_trust", &cfg.Skills.ProjectTrust, ProjectTrustAsk},
		{"scheduler.project_trust", &cfg.Scheduler.ProjectTrust, ProjectTrustAsk},
	} {
		if *p.value == ProjectTrustAllow {
			*p.value = p.ask
			dropped = append(dropped, p.key)
		}
	}
	if len(cfg.Tools.CommandAllowlist) > 0 {
		cfg.Tools.CommandAllowlist = nil
		dropped = append(dropped, "tools.command_allowlist")
	}
	for _, key := range dropped {
		slog.Default().Warn("config: the workspace's config.yaml, read because the Coddy home has none, cannot widen what a checkout may run; the key is not used (put it in the home's config.yaml or name the file with --config)", "key", key, "config", cfg.Paths.ConfigPath, "home", cfg.Paths.Home)
	}
}
