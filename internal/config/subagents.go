package config

import (
	"fmt"
	"strings"
)

// Trust policy values for subagents.project_trust. They govern definitions
// that live inside the session workspace (project scope), which arrive with
// the checkout and therefore cannot be assumed to be the operator's own.
const (
	// SubagentsProjectTrustAsk loads project-scope definitions but refuses to
	// spawn one until the operator approved that exact file for that workspace
	// (coddy agents trust, POST /coddy/subagents/{name}/trust).
	SubagentsProjectTrustAsk = "ask"
	// SubagentsProjectTrustAllow treats project-scope definitions like the
	// operator's own files under the coddy home.
	SubagentsProjectTrustAllow = "allow"
	// SubagentsProjectTrustDeny never reads project-scope directories.
	SubagentsProjectTrustDeny = "deny"
)

// Defaults for subagents.
const (
	// SubagentsDefaultMaxConcurrent bounds how many subagent runs the whole
	// process may have in flight at once, whatever session started them.
	SubagentsDefaultMaxConcurrent = 4
	// SubagentsDefaultMaxDepth allows a session to spawn but keeps the child
	// from spawning again.
	SubagentsDefaultMaxDepth = 1
	// SubagentsDefaultTimeoutSeconds is the hard limit for one run when neither
	// the definition nor the call gives one.
	SubagentsDefaultTimeoutSeconds = 1800
)

// Subagents is the YAML subagents section (key subagents): where subagent
// definitions come from and how the pool that runs them is bounded.
type Subagents struct {
	// Enabled registers the spawn_agent tool and renders the catalog. Unset
	// means enabled.
	Enabled *bool `yaml:"enable"`

	// Dirs lists definition directories, lowest priority first; later entries
	// override earlier ones by name. ${CODDY_HOME} expands at load time, ${CWD}
	// per session.
	Dirs []string `yaml:"dirs"`

	// ProjectTrust decides what a definition found inside the workspace may
	// do: ask (default), allow, or deny.
	ProjectTrust string `yaml:"project_trust"`

	// MaxConcurrent caps subagent runs in flight across the process.
	MaxConcurrent int `yaml:"max_concurrent"`

	// MaxDepth bounds nesting: 1 lets a session spawn but not its children.
	// A nil pointer means the default; an explicit 0 forbids spawning at all.
	MaxDepth *int `yaml:"max_depth"`

	// DefaultTimeoutSeconds is the hard limit for a run whose definition and
	// call give none.
	DefaultTimeoutSeconds int `yaml:"default_timeout_seconds"`

	// MaxTurns caps a child's ReAct rounds; 0 follows agent.max_turns.
	MaxTurns int `yaml:"max_turns"`
}

// DefaultSubagentDirs are the definition directories every workspace reads,
// whatever subagents.dirs says, lowest priority first - the same layout as
// the skill folders (DefaultSkillDirs): the user's agents definitions, the
// project's .agents/agents, Coddy's own, the project's .coddy/agents. A name
// found in several is taken from the last. The two project folders are
// project scope and follow subagents.project_trust. The placeholders stay in
// the entries for the loader to expand per session.
func DefaultSubagentDirs() []string {
	return []string{
		"${HOME}/.agents/agents",
		"${CWD}/.agents/agents",
		"${CODDY_HOME}/agents",
		"${CWD}/.coddy/agents",
	}
}

// SearchDirs is every definition directory a workspace reads, lowest priority
// first: DefaultSubagentDirs, then the extra directories of subagents.dirs in
// their order, which win a name over the defaults. Every reader of
// definitions goes through it. A folder named twice is read once, at its last
// place (the loader drops the earlier one).
func (s Subagents) SearchDirs() []string {
	return append(DefaultSubagentDirs(), s.Dirs...)
}

// ApplyDefaults leaves Dirs as the file has it (subagents.dirs only adds to
// DefaultSubagentDirs, see SearchDirs) and expands ${CODDY_HOME} in the
// operator's entries at load time; ${CWD} stays for the session.
func (s *Subagents) ApplyDefaults(p Paths) {
	if len(s.Dirs) == 0 {
		return
	}
	for i := range s.Dirs {
		s.Dirs[i] = ExpandCODDYHomeOnly(strings.TrimSpace(s.Dirs[i]), p)
	}
}

// ResolvedEnabled reports whether subagents are offered, defaulting to true
// when the field is unset or the section is nil.
func (s *Subagents) ResolvedEnabled() bool {
	if s == nil || s.Enabled == nil {
		return true
	}
	return *s.Enabled
}

// ResolvedProjectTrust returns ProjectTrust with a safe default of ask, so an
// empty or unknown value never widens the policy.
func (s Subagents) ResolvedProjectTrust() string {
	switch v := strings.ToLower(strings.TrimSpace(s.ProjectTrust)); v {
	case SubagentsProjectTrustAsk, SubagentsProjectTrustAllow, SubagentsProjectTrustDeny:
		return v
	default:
		return SubagentsProjectTrustAsk
	}
}

// EffectiveMaxConcurrent returns max_concurrent with the default applied.
func (s Subagents) EffectiveMaxConcurrent() int {
	if s.MaxConcurrent <= 0 {
		return SubagentsDefaultMaxConcurrent
	}
	return s.MaxConcurrent
}

// EffectiveMaxDepth returns max_depth with the default applied. An explicit 0
// stays 0: the operator asked that nothing spawns.
func (s Subagents) EffectiveMaxDepth() int {
	if s.MaxDepth == nil {
		return SubagentsDefaultMaxDepth
	}
	if *s.MaxDepth < 0 {
		return 0
	}
	return *s.MaxDepth
}

// EffectiveDefaultTimeoutSeconds returns default_timeout_seconds with the
// default applied.
func (s Subagents) EffectiveDefaultTimeoutSeconds() int {
	if s.DefaultTimeoutSeconds <= 0 {
		return SubagentsDefaultTimeoutSeconds
	}
	return s.DefaultTimeoutSeconds
}

// EffectiveMaxTurns returns the ReAct cap for a child: the section's own
// max_turns, else agent.max_turns; 0 when neither sets one, which is no step
// limit (the run's timeout still bounds it).
func (s Subagents) EffectiveMaxTurns(agentMaxTurns int) int {
	if s.MaxTurns > 0 {
		return s.MaxTurns
	}
	return max(agentMaxTurns, 0)
}

// Validate normalises project_trust and rejects negative knobs; zero keeps
// meaning "use the default" everywhere except max_depth, which is a pointer.
func (s *Subagents) Validate() error {
	v := strings.ToLower(strings.TrimSpace(s.ProjectTrust))
	if v == "" {
		v = SubagentsProjectTrustAsk
	}
	switch v {
	case SubagentsProjectTrustAsk, SubagentsProjectTrustAllow, SubagentsProjectTrustDeny:
		s.ProjectTrust = v
	default:
		return fmt.Errorf("subagents.project_trust: must be one of %q, %q, %q (got %q)",
			SubagentsProjectTrustAsk, SubagentsProjectTrustAllow, SubagentsProjectTrustDeny, s.ProjectTrust)
	}
	for name, n := range map[string]int{
		"max_concurrent":          s.MaxConcurrent,
		"default_timeout_seconds": s.DefaultTimeoutSeconds,
		"max_turns":               s.MaxTurns,
	} {
		if n < 0 {
			return fmt.Errorf("subagents.%s: must be >= 0", name)
		}
	}
	if s.MaxDepth != nil && *s.MaxDepth < 0 {
		return fmt.Errorf("subagents.max_depth: must be >= 0")
	}
	return nil
}
