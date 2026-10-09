package config

import (
	"fmt"
	"time"
)

const (
	// SupervisorDefaultStallSeconds matches agent.llm_stream_idle_timeout_ms:
	// a model call that stays silent longer is cut there anyway, and a
	// shorter limit would cut a slow non-streaming answer that is on its way.
	SupervisorDefaultStallSeconds     = 300
	SupervisorDefaultMaxNudges        = 2
	SupervisorDefaultMaxContinuations = 10
	SupervisorDefaultLoopRepeat       = 3
)

// Supervisor configures the session supervisor: the /goal checks, the
// automatic continuations and the watchdog for stalled or looping turns
// (docs/features/session-supervisor.md).
type Supervisor struct {
	// Enable checks ordinary turns against the latest request even without a
	// goal; a goal is checked whatever it says.
	Enable bool `yaml:"enable" json:"enable,omitempty"`
	// Model is the models[].model that checks the work; empty uses the
	// session's own model, which grades its own work (a model of another
	// family is the better judge).
	Model string `yaml:"model" json:"model,omitempty"`
	// Verify has a met verdict confirmed by the goal-verifier subagent, which
	// reads the workspace with read-only tools, before a goal is closed.
	// Unset means true.
	Verify           *bool `yaml:"verify" json:"verify,omitempty"`
	StallSeconds     *int  `yaml:"stall_seconds" json:"stall_seconds,omitempty"`
	MaxNudges        *int  `yaml:"max_nudges" json:"max_nudges,omitempty"`
	MaxContinuations *int  `yaml:"max_continuations" json:"max_continuations,omitempty"`
	LoopRepeat       *int  `yaml:"loop_repeat" json:"loop_repeat,omitempty"`
	// TokenBudget caps the uncached input plus output tokens one goal's turns
	// may spend; 0 or unset is no cap.
	TokenBudget *int `yaml:"token_budget" json:"token_budget,omitempty"`
}

// clone copies the pointers, so two configurations never share a bound.
func (s Supervisor) clone() Supervisor {
	s.Verify = cloneBoolPtr(s.Verify)
	s.StallSeconds = cloneIntPtr(s.StallSeconds)
	s.MaxNudges = cloneIntPtr(s.MaxNudges)
	s.MaxContinuations = cloneIntPtr(s.MaxContinuations)
	s.LoopRepeat = cloneIntPtr(s.LoopRepeat)
	s.TokenBudget = cloneIntPtr(s.TokenBudget)
	return s
}

func effectiveSupervisorInt(value *int, fallback int) int {
	if value != nil {
		return *value
	}
	return fallback
}

// StallTimeout is how long a goal turn may go without any update before the
// watchdog cuts it; zero turns the timer off.
func (s Supervisor) StallTimeout() time.Duration {
	return time.Duration(effectiveSupervisorInt(s.StallSeconds, SupervisorDefaultStallSeconds)) * time.Second
}

// ContinuationLimit is how many automatic turns one goal may use.
func (s Supervisor) ContinuationLimit() int {
	return effectiveSupervisorInt(s.MaxContinuations, SupervisorDefaultMaxContinuations)
}

// NudgeLimit is how many recovery turns (after a stall, a loop or a failed
// turn) one run of the supervisor may start.
func (s Supervisor) NudgeLimit() int {
	return effectiveSupervisorInt(s.MaxNudges, SupervisorDefaultMaxNudges)
}

// LoopRepeatLimit is how many times a repeating cycle of tool operations may
// come round before the watchdog cuts the turn; zero turns it off.
func (s Supervisor) LoopRepeatLimit() int {
	return effectiveSupervisorInt(s.LoopRepeat, SupervisorDefaultLoopRepeat)
}

// VerifyEnabled says whether a met verdict is confirmed against the workspace.
func (s Supervisor) VerifyEnabled() bool {
	return s.Verify == nil || *s.Verify
}

// EffectiveTokenBudget is the token cap of one goal, 0 for none.
func (s Supervisor) EffectiveTokenBudget() int {
	return max(effectiveSupervisorInt(s.TokenBudget, 0), 0)
}

// Validate checks the bounds and that the model is configured.
func (s Supervisor) Validate(cfg *Config) error {
	for name, value := range map[string]*int{
		"stall_seconds":     s.StallSeconds,
		"max_nudges":        s.MaxNudges,
		"max_continuations": s.MaxContinuations,
		"loop_repeat":       s.LoopRepeat,
		"token_budget":      s.TokenBudget,
	} {
		if value != nil && *value < 0 {
			return fmt.Errorf("supervisor.%s must be non-negative", name)
		}
	}
	if s.Model != "" {
		if _, err := cfg.ResolveLLM(s.Model); err != nil {
			return fmt.Errorf("supervisor.model: %w", err)
		}
	}
	return nil
}
