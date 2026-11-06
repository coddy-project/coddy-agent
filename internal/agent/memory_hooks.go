//go:build memory

package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/external/memory"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// registerMemoryChildTools gives the memory subagent its tools: the six
// coddy_memory_* tools over the two note roots of the session's cwd, added to
// this Agent's registry when the session is the system memory child. An
// ordinary session never gets them, so the main model never sees a memory
// tool; the child's own allowlist decides which of the six it may call.
func (a *Agent) registerMemoryChildTools() {
	if a.subagent == nil || a.subagent.Kind != session.SubagentKindMemory {
		return
	}
	tools, err := memory.Tools(a.cfg, a.state.GetCWD())
	if err != nil {
		a.log.Warn("memory subagent tools unavailable", "error", err)
		return
	}
	for _, t := range tools {
		a.registry.Register(t)
	}
}

// memoryFallbackModels is the chain behind the memory model: the configured
// fallbacks, then the session's own model as the last resort. A model an acp
// agent serves is never in it.
func memoryFallbackModels(cfg *config.Config, configured []string, sessionModel string) []string {
	out := make([]string, 0, len(configured)+1)
	for _, m := range configured {
		if m = strings.TrimSpace(m); m != "" && !cfg.ModelRunsACPAgent(m) {
			out = append(out, m)
		}
	}
	if sessionModel = strings.TrimSpace(sessionModel); sessionModel != "" && !cfg.ModelRunsACPAgent(sessionModel) {
		out = append(out, sessionModel)
	}
	return out
}

// memorySpawnCheck gates a spawn_agent call made under the bundled memory
// definition. It needs the memory feature on: a memory run against no roots
// would answer from nothing. Without the tag the stub answers an error that
// names it instead.
func (a *Agent) memorySpawnCheck(cfg *config.Config) error {
	if !cfg.Memory.Enabled {
		return fmt.Errorf("the memory subagent is off: enable it under memory.enable")
	}
	return nil
}

// memorySpawnVisible says whether the bundled memory definition reaches the
// model-facing catalog: while the feature is off the definition is hidden,
// so the parent never sees a spawn that memorySpawnCheck would refuse. The
// stub without the memory tag hides it always.
func (a *Agent) memorySpawnVisible(cfg *config.Config) bool {
	return cfg.Memory.Enabled
}

// memorySpawnSpec turns the spec of a spawn made under the bundled memory
// definition into a run of the memory subagent: its own six tools (three in
// a read-only session), its prompt template, the memory model and its
// fallback chain, the operator's addendum and the guides of the memory roots
// ahead of the task. The task is whatever prompt the parent passed, hooks
// context included, quoted under the guides. The child always runs in agent
// mode: its allowlist is the fixed memory set, and a plan or ask parent's
// tool policy would empty it; the recall-only set covers the read-only case.
func (a *Agent) memorySpawnSpec(cfg *config.Config, spec *session.SubagentSpec, prompt *string, parentMode, sessionModel string) {
	readOnly := parentMode == string(session.ModePlan) || parentMode == string(session.ModeAsk)
	spec.Kind = session.SubagentKindMemory
	spec.Mode = string(session.ModeAgent)
	spec.PromptTemplate = memory.PromptTemplate(readOnly)
	spec.Tools = memory.ToolNames(readOnly)
	addendum, cut := cfg.Memory.EffectiveAdditionalPrompt()
	if cut {
		a.log.Warn("memory.additional_prompt is longer than additional_prompt_max_chars; the memory subagent reads the first characters only",
			"session_id", spec.ParentSessionID, "max_chars", cfg.Memory.AdditionalPromptMaxChars, "chars", utf8.RuneCountInString(cfg.Memory.AdditionalPrompt))
	}
	spec.Role = addendum
	spec.MaxTokens = cfg.Memory.CopilotMaxTokens
	spec.MaxTurns = cfg.Memory.EffectiveMaxTurns()
	if m := strings.TrimSpace(cfg.Memory.Model); m != "" && cfg.FindModelEntry(m) != nil {
		spec.SelectedModelID = m
	}
	spec.FallbackModels = memoryFallbackModels(cfg, cfg.Memory.FallbackModels, sessionModel)
	guides, err := memory.Guides(cfg, spec.CWD)
	if err != nil {
		a.log.Warn("memory guide not read", "session_id", spec.ParentSessionID, "error", err)
	}
	*prompt = memory.TaskMessage(*prompt, guides)
}
