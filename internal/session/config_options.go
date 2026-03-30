package session

import (
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// BuildACPConfigOptions returns Session Config Options for the ACP protocol (mode + model selectors).
func BuildACPConfigOptions(cfg *config.Config, state *State) []acp.ConfigOption {
	mode := state.GetMode()
	if mode == "" {
		mode = string(ModeAgent)
	}

	modeOpt := acp.ConfigOption{
		ID:           "mode",
		Name:         "Session mode",
		Description:  "Agent runs tools; Plan focuses on design without execution.",
		Category:     "mode",
		Type:         "select",
		CurrentValue: mode,
		Options: []acp.ConfigOptionValue{
			{Value: string(ModeAgent), Name: "Agent", Description: "Execute tasks with full tool access"},
			{Value: string(ModePlan), Name: "Plan", Description: "Plan and design without code execution"},
		},
	}

	out := []acp.ConfigOption{modeOpt}
	if len(cfg.Models.Defs) == 0 {
		return out
	}

	opts := make([]acp.ConfigOptionValue, 0, len(cfg.Models.Defs))
	for _, d := range cfg.Models.Defs {
		name := d.Model
		if name == "" {
			name = d.ID
		}
		opts = append(opts, acp.ConfigOptionValue{
			Value:       d.ID,
			Name:        name,
			Description: d.Provider,
		})
	}

	current := state.EffectiveModelID(cfg)
	modelOpt := acp.ConfigOption{
		ID:           "model",
		Name:         "Model",
		Description:  "LLM used for this session.",
		Category:     "model",
		Type:         "select",
		CurrentValue: current,
		Options:      opts,
	}
	return append(out, modelOpt)
}
