package config

import (
	"fmt"
	"time"
)

const (
	SupervisorDefaultStallSeconds     = 180
	SupervisorDefaultMaxNudges        = 2
	SupervisorDefaultMaxContinuations = 3
	SupervisorDefaultLoopRepeat       = 3
)

// Supervisor configures goal evaluation and watchdog continuations.
type Supervisor struct {
	Enable           bool   `yaml:"enable" json:"enable,omitempty"`
	Model            string `yaml:"model" json:"model,omitempty"`
	StallSeconds     *int   `yaml:"stall_seconds" json:"stall_seconds,omitempty"`
	MaxNudges        *int   `yaml:"max_nudges" json:"max_nudges,omitempty"`
	MaxContinuations *int   `yaml:"max_continuations" json:"max_continuations,omitempty"`
	LoopRepeat       *int   `yaml:"loop_repeat" json:"loop_repeat,omitempty"`
}

func effectiveSupervisorInt(value *int, fallback int) int {
	if value != nil {
		return *value
	}
	return fallback
}

func (s Supervisor) StallTimeout() time.Duration {
	return time.Duration(effectiveSupervisorInt(s.StallSeconds, SupervisorDefaultStallSeconds)) * time.Second
}

func (s Supervisor) ContinuationLimit() int {
	return effectiveSupervisorInt(s.MaxContinuations, SupervisorDefaultMaxContinuations)
}

func (s Supervisor) NudgeLimit() int {
	return effectiveSupervisorInt(s.MaxNudges, SupervisorDefaultMaxNudges)
}

func (s Supervisor) LoopRepeatLimit() int {
	return effectiveSupervisorInt(s.LoopRepeat, SupervisorDefaultLoopRepeat)
}

func (s Supervisor) Validate(cfg *Config) error {
	for name, value := range map[string]*int{
		"stall_seconds":     s.StallSeconds,
		"max_nudges":        s.MaxNudges,
		"max_continuations": s.MaxContinuations,
		"loop_repeat":       s.LoopRepeat,
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
