package config

import (
	"strings"
	"testing"
	"time"
)

func TestSupervisorDefaultsAndExplicitZero(t *testing.T) {
	var cfg Supervisor
	if cfg.StallTimeout() != 180*time.Second || cfg.NudgeLimit() != 2 || cfg.ContinuationLimit() != 3 || cfg.LoopRepeatLimit() != 3 {
		t.Fatalf("defaults = %+v", cfg)
	}
	zero := 0
	cfg = Supervisor{StallSeconds: &zero, MaxNudges: &zero, MaxContinuations: &zero, LoopRepeat: &zero}
	if cfg.StallTimeout() != 0 || cfg.NudgeLimit() != 0 || cfg.ContinuationLimit() != 0 || cfg.LoopRepeatLimit() != 0 {
		t.Fatalf("explicit zero ignored: %+v", cfg)
	}
}

func TestSupervisorConfigJSONRoundTrip(t *testing.T) {
	two := 2
	cfg := &Config{Supervisor: Supervisor{Enable: true, Model: "p/model", MaxContinuations: &two}}
	got := JSONDTOToConfig(ConfigToJSONDTO(cfg), Paths{})
	if !got.Supervisor.Enable || got.Supervisor.Model != "p/model" || got.Supervisor.ContinuationLimit() != 2 {
		t.Fatalf("supervisor lost in JSON: %+v", got.Supervisor)
	}
}

func TestSupervisorRejectsNegativeBounds(t *testing.T) {
	negative := -1
	for name, cfg := range map[string]Supervisor{
		"stall_seconds":     {StallSeconds: &negative},
		"max_nudges":        {MaxNudges: &negative},
		"max_continuations": {MaxContinuations: &negative},
		"loop_repeat":       {LoopRepeat: &negative},
	} {
		if err := cfg.Validate(&Config{}); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}
