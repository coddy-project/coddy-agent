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
		Description:  "Agent runs tools; Plan focuses on design without execution; Ask answers with read-only research tools.",
		Category:     "mode",
		Type:         "select",
		CurrentValue: mode,
		Options: []acp.ConfigOptionValue{
			{Value: string(ModeAgent), Name: "Agent", Description: "Execute tasks with full tool access"},
			{Value: string(ModePlan), Name: "Plan", Description: "Plan and design without code execution"},
			{Value: string(ModeAsk), Name: "Ask", Description: "Answer questions with read-only research tools"},
		},
	}

	out := []acp.ConfigOption{modeOpt}
	if len(cfg.Models) > 0 {
		opts := make([]acp.ConfigOptionValue, 0, len(cfg.Models))
		for _, d := range cfg.Models {
			name := d.Model
			desc := ""
			if p := cfg.FindProvider(d.ProviderName()); p != nil {
				desc = p.Type
			}
			opts = append(opts, acp.ConfigOptionValue{
				Value:       d.Model,
				Name:        name,
				Description: desc,
			})
		}

		current := state.SessionModelID(cfg)
		modelOpt := acp.ConfigOption{
			ID:           "model",
			Name:         "Model",
			Description:  "LLM used for this session.",
			Category:     "model",
			Type:         "select",
			CurrentValue: current,
			Options:      opts,
		}
		out = append(out, modelOpt)

		if ent := cfg.FindModelEntry(current); ent != nil {
			levels := cfg.ReasoningChoicesFor(ent)
			if len(levels) > 0 {
				reasoningOptions := make([]acp.ConfigOptionValue, 0, len(levels))
				for _, level := range levels {
					reasoningOptions = append(reasoningOptions, acp.ConfigOptionValue{
						Value: level,
						Name:  level,
					})
				}
				out = append(out, acp.ConfigOption{
					ID:          "reasoning",
					Name:        "Reasoning",
					Description: "Controls the reasoning effort used for this session.",
					// thought_level is the ACP category for a reasoning effort.
					Category:     "thought_level",
					Type:         "select",
					CurrentValue: state.SessionReasoning(cfg),
					Options:      reasoningOptions,
				})
			}
		}
	}

	if opt, ok := ToolsetOption(cfg, state); ok {
		out = append(out, opt)
	}

	effectivePerm := state.GetPermissionMode()
	if effectivePerm == "" {
		effectivePerm = cfg.Tools.ResolvedPermMode()
	}
	return append(out, PermissionModeOption(effectivePerm))
}

// ToolsetOption is the ACP config option of the session's toolset: the
// toolsets[] presets and default, with the one the session runs with as its
// value. There is none while the configuration has no preset, so an editor or
// a console shows no toolset selector until one is added.
func ToolsetOption(cfg *config.Config, state *State) (acp.ConfigOption, bool) {
	names := cfg.ToolsetNames()
	if len(names) == 0 {
		return acp.ConfigOption{}, false
	}
	opts := make([]acp.ConfigOptionValue, 0, len(names)+1)
	for _, n := range names {
		opts = append(opts, acp.ConfigOptionValue{Value: n, Name: n})
	}
	opts = append(opts, acp.ConfigOptionValue{Value: config.ToolsetDefault, Name: config.ToolsetDefault,
		Description: "Every tool the mode offers"})
	return acp.ConfigOption{
		ID:          "toolset",
		Name:        "Toolset",
		Description: "The preset of tools this session is offered.",
		// An agent's own option, like the permission mode's.
		Category:     "_toolset",
		Type:         "select",
		CurrentValue: state.SessionToolset(cfg),
		Options:      opts,
	}, true
}

// PermissionModeOption is the ACP config option of a session's permission
// mode with current as its value: what an editor shows as a selector and a
// console reads for its footer on entering the session.
func PermissionModeOption(current string) acp.ConfigOption {
	return acp.ConfigOption{
		ID:          "permission_mode",
		Name:        "Permission mode",
		Description: "Controls when the agent asks for user approval before running tools.",
		// Not one of the protocol's categories: a leading underscore is the
		// namespace ACP leaves to an agent's own options.
		Category:     "_permission_mode",
		Type:         "select",
		CurrentValue: current,
		Options: []acp.ConfigOptionValue{
			{Value: config.PermModeAsk, Name: "Ask", Description: "Always ask before running commands or writing files"},
			{Value: config.PermModeAcceptEdits, Name: "Accept edits", Description: "Auto-approve file writes; ask before running commands"},
			{Value: config.PermModeBypass, Name: "Bypass", Description: "Never ask for permission"},
		},
	}
}
