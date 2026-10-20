package agent

import (
	"maps"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// webSearchSettings resolves tools.websearch for the tool environment. It is
// re-read wherever the environment is built or refreshed, so an operator who
// changes the engine list does not have to restart to be searched for.
func webSearchSettings(cfg *config.Config) *tooling.WebSearchSettings {
	if cfg == nil {
		return nil
	}
	resolved := cfg.Tools.WebSearch.ToolSettings()
	out := tooling.WebSearchSettings(resolved)
	return &out
}

// previewServerSettings resolves tools.preview_server for the tool layer, the
// same way webSearchSettings does for the search tool.
func previewServerSettings(cfg *config.Config) *tooling.PreviewServerSettings {
	if cfg == nil {
		return nil
	}
	out := tooling.PreviewServerSettings(cfg.Tools.PreviewServer.ToolSettings())
	return &out
}

// httpRequestEnv puts tools.http_request on the tool environment: the
// destinations a request reaches without asking and the headers every request
// sends. It runs wherever the environment is built or refreshed after a config
// reload, and it copies, so a reload never edits what a call is reading.
func httpRequestEnv(env *tooling.Env, cfg *config.Config) {
	if env == nil || cfg == nil {
		return
	}
	env.HTTPAllowlist = append([]string(nil), cfg.Tools.HTTPRequest.Allowlist...)
	env.HTTPDefaultHeaders = maps.Clone(cfg.Tools.HTTPRequest.DefaultHeaders)
}
