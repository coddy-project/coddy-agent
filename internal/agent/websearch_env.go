package agent

import (
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
