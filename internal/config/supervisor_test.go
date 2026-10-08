package config

import (
	"strings"
	"testing"
	"time"
)

func TestSupervisorDefaultsAndExplicitZero(t *testing.T) {
	var cfg Supervisor
	if cfg.StallTimeout() != 300*time.Second || cfg.NudgeLimit() != 2 || cfg.ContinuationLimit() != 10 ||
		cfg.LoopRepeatLimit() != 3 || !cfg.VerifyEnabled() || cfg.EffectiveTokenBudget() != 0 {
		t.Fatalf("defaults = %+v", cfg)
	}
	zero, off := 0, false
	cfg = Supervisor{StallSeconds: &zero, MaxNudges: &zero, MaxContinuations: &zero, LoopRepeat: &zero, Verify: &off}
	if cfg.StallTimeout() != 0 || cfg.NudgeLimit() != 0 || cfg.ContinuationLimit() != 0 || cfg.LoopRepeatLimit() != 0 || cfg.VerifyEnabled() {
		t.Fatalf("explicit zero ignored: %+v", cfg)
	}
}

func TestSupervisorConfigJSONRoundTrip(t *testing.T) {
	two, budget, off := 2, 50000, false
	cfg := &Config{Supervisor: Supervisor{Enable: true, Model: "p/model", MaxContinuations: &two, TokenBudget: &budget, Verify: &off}}
	got := JSONDTOToConfig(ConfigToJSONDTO(cfg), Paths{})
	if !got.Supervisor.Enable || got.Supervisor.Model != "p/model" || got.Supervisor.ContinuationLimit() != 2 ||
		got.Supervisor.EffectiveTokenBudget() != 50000 || got.Supervisor.VerifyEnabled() {
		t.Fatalf("supervisor lost in JSON: %+v", got.Supervisor)
	}
	// The copy owns its bounds: changing it leaves the original alone.
	*got.Supervisor.MaxContinuations = 7
	if cfg.Supervisor.ContinuationLimit() != 2 {
		t.Fatal("the JSON round trip shares a pointer with the original")
	}
}

func TestSupervisorRejectsNegativeBounds(t *testing.T) {
	negative := -1
	for name, cfg := range map[string]Supervisor{
		"stall_seconds":     {StallSeconds: &negative},
		"max_nudges":        {MaxNudges: &negative},
		"max_continuations": {MaxContinuations: &negative},
		"loop_repeat":       {LoopRepeat: &negative},
		"token_budget":      {TokenBudget: &negative},
	} {
		if err := cfg.Validate(&Config{}); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}
