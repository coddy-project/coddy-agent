package config

import "fmt"

// Defaults for the ReAct loop when YAML omits zero values.
const (
	AgentDefaultMaxTurns         = 30
	AgentDefaultMaxTokensPerTurn = 200000
	AgentDefaultLLMRetryMax      = 3
	AgentDefaultLLMRetryBaseMS   = 1000
	// AgentDefaultLoopToolRepeatLimit is how many consecutive identical tool calls
	// (same name, same canonical arguments) the loop guard tolerates.
	AgentDefaultLoopToolRepeatLimit = 3
	// AgentDefaultLoopStreamRepeatCycles is how many identical back-to-back output
	// cycles inside one streamed response trip the loop guard.
	AgentDefaultLoopStreamRepeatCycles = 5
	// AgentDefaultLoopNudgeMax is how many times a turn may be nudged back on track
	// before the loop guard stops it.
	AgentDefaultLoopNudgeMax = 2
)

// Agent is the YAML agent section (key agent) for ReAct loop settings.
type Agent struct {
	// Model is the models[].id used for LLM calls until the session overrides the model in the client.
	Model            string `yaml:"model"`
	MaxTurns         int    `yaml:"max_turns"`
	MaxTokensPerTurn int    `yaml:"max_tokens_per_turn"`
	// LLMRetryMax is the number of retries after a retryable LLM error such as HTTP 429 (default 3).
	LLMRetryMax      int `yaml:"llm_retry_max"`
	// LLMRetryBaseMS is the initial backoff between LLM retries in milliseconds (default 1000).
	LLMRetryBaseMS   int `yaml:"llm_retry_base_ms"`
	// LLMMinIntervalMS enforces a minimum gap between consecutive LLM calls in milliseconds (default 0).
	LLMMinIntervalMS int `yaml:"llm_min_interval_ms"`
	// LoopGuard toggles runaway-loop protection: aborting a streamed response that
	// degenerates into repeating itself, and blocking identical tool calls issued
	// over and over. A nil pointer means the default (true).
	LoopGuard *bool `yaml:"loop_guard"`
	// LoopToolRepeatLimit is how many consecutive identical tool calls trip the guard.
	// A nil pointer means the default (3); an explicit 0 disables the tool-repeat check.
	LoopToolRepeatLimit *int `yaml:"loop_tool_repeat_limit"`
	// LoopStreamRepeatCycles is how many identical back-to-back output cycles inside one
	// streamed response trip the guard. A nil pointer means the default (5); an explicit
	// 0 disables the stream check.
	LoopStreamRepeatCycles *int `yaml:"loop_stream_repeat_cycles"`
	// LoopNudgeMax is how many times one turn may be nudged back on track before the
	// guard stops it with a notice. A nil pointer means the default (2); an explicit 0
	// stops the turn on the first detected loop.
	LoopNudgeMax *int `yaml:"loop_nudge_max"`
}

// LoopGuardEnabled reports whether runaway-loop protection is active. Defaults to true when unset.
func (c *Agent) LoopGuardEnabled() bool {
	return c.LoopGuard == nil || *c.LoopGuard
}

// EffectiveLoopToolRepeatLimit returns loop_tool_repeat_limit with the default applied.
func (c *Agent) EffectiveLoopToolRepeatLimit() int {
	if c.LoopToolRepeatLimit == nil {
		return AgentDefaultLoopToolRepeatLimit
	}
	return *c.LoopToolRepeatLimit
}

// EffectiveLoopStreamRepeatCycles returns loop_stream_repeat_cycles with the default applied.
func (c *Agent) EffectiveLoopStreamRepeatCycles() int {
	if c.LoopStreamRepeatCycles == nil {
		return AgentDefaultLoopStreamRepeatCycles
	}
	return *c.LoopStreamRepeatCycles
}

// EffectiveLoopNudgeMax returns loop_nudge_max with the default applied.
func (c *Agent) EffectiveLoopNudgeMax() int {
	if c.LoopNudgeMax == nil {
		return AgentDefaultLoopNudgeMax
	}
	return *c.LoopNudgeMax
}

// ApplyDefaults sets MaxTurns and MaxTokensPerTurn when they are zero.
func (c *Agent) ApplyDefaults() {
	if c.MaxTurns == 0 {
		c.MaxTurns = AgentDefaultMaxTurns
	}
	if c.MaxTokensPerTurn == 0 {
		c.MaxTokensPerTurn = AgentDefaultMaxTokensPerTurn
	}
	if c.LLMRetryMax == 0 {
		c.LLMRetryMax = AgentDefaultLLMRetryMax
	}
	if c.LLMRetryBaseMS == 0 {
		c.LLMRetryBaseMS = AgentDefaultLLMRetryBaseMS
	}
}

// Validate checks bounds after defaults.
func (c *Agent) Validate() error {
	if c.MaxTurns < 0 {
		return fmt.Errorf("agent.max_turns: must be >= 0")
	}
	if c.MaxTokensPerTurn < 0 {
		return fmt.Errorf("agent.max_tokens_per_turn: must be >= 0")
	}
	if c.LLMRetryMax < 0 {
		return fmt.Errorf("agent.llm_retry_max: must be >= 0")
	}
	if c.LLMRetryBaseMS < 0 {
		return fmt.Errorf("agent.llm_retry_base_ms: must be >= 0")
	}
	if c.LLMMinIntervalMS < 0 {
		return fmt.Errorf("agent.llm_min_interval_ms: must be >= 0")
	}
	if c.LoopToolRepeatLimit != nil && *c.LoopToolRepeatLimit < 0 {
		return fmt.Errorf("agent.loop_tool_repeat_limit: must be >= 0")
	}
	if c.LoopStreamRepeatCycles != nil && *c.LoopStreamRepeatCycles < 0 {
		return fmt.Errorf("agent.loop_stream_repeat_cycles: must be >= 0")
	}
	if c.LoopNudgeMax != nil && *c.LoopNudgeMax < 0 {
		return fmt.Errorf("agent.loop_nudge_max: must be >= 0")
	}
	return nil
}
