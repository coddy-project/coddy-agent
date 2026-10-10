package config

import (
	"strings"
	"testing"
)

func TestInTurnDefaults(t *testing.T) {
	var c Compaction
	c.ApplyDefaults()
	if !c.InTurn.IsEnabled() {
		t.Fatal("in-turn compaction must be on by default")
	}
	if got := c.InTurn.EffectiveKeepRecentSteps(); got != InTurnDefaultKeepRecentSteps {
		t.Fatalf("keep_recent_steps = %d, want %d", got, InTurnDefaultKeepRecentSteps)
	}
	if InTurnDefaultKeepRecentSteps != 4 {
		t.Fatalf("the documented default is 4, the constant says %d", InTurnDefaultKeepRecentSteps)
	}
	// A config built without ApplyDefaults reads the same.
	if !(&InTurn{}).IsEnabled() || (&InTurn{}).EffectiveKeepRecentSteps() != 4 {
		t.Fatal("the zero value must mean the defaults")
	}
}

func TestInTurnExplicitValues(t *testing.T) {
	off := false
	two := 2
	in := InTurn{Enabled: &off, KeepRecentSteps: &two}
	if in.IsEnabled() {
		t.Fatal("enable: false must switch the fold off")
	}
	if got := in.EffectiveKeepRecentSteps(); got != 2 {
		t.Fatalf("keep_recent_steps = %d, want 2", got)
	}
}

func TestInTurnValidate(t *testing.T) {
	zero, neg, one, many := 0, -1, 1, 40
	cases := []struct {
		name    string
		in      InTurn
		wantErr bool
	}{
		{name: "defaults", in: InTurn{}},
		{name: "one step is the smallest", in: InTurn{KeepRecentSteps: &one}},
		{name: "a large count", in: InTurn{KeepRecentSteps: &many}},
		{name: "zero is refused: the latest step always stays", in: InTurn{KeepRecentSteps: &zero}, wantErr: true},
		{name: "negative is refused", in: InTurn{KeepRecentSteps: &neg}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			err := in.Validate()
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "compaction.in_turn.keep_recent_steps") || !strings.Contains(err.Error(), ">= 1") {
				t.Fatalf("error = %v, want one naming compaction.in_turn.keep_recent_steps and >= 1", err)
			}
		})
	}

	// The section's Validate is reached through the compaction one, so a bad
	// value is refused at load.
	c := Compaction{InTurn: InTurn{KeepRecentSteps: &zero}}
	c.ApplyDefaults()
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "in_turn.keep_recent_steps") {
		t.Fatalf("Compaction.Validate must surface in_turn errors, got %v", err)
	}
}

// An absent key stays nil (the default) and an explicit value is read back as
// written, including enable: false.
func TestInTurnYAML(t *testing.T) {
	load := func(extra string) (*Config, error) {
		return parseValidateYAMLBytes(`
providers:
  - name: fake
    type: openai
    api_key: k
models:
  - model: fake/m
agent:
  model: fake/m
compaction:
`+extra, Paths{})
	}

	absent, err := load("  threshold_percent: 70\n")
	if err != nil {
		t.Fatal(err)
	}
	if absent.Compaction.InTurn.Enabled != nil || absent.Compaction.InTurn.KeepRecentSteps != nil {
		t.Fatalf("absent keys must stay nil: %+v", absent.Compaction.InTurn)
	}
	if !absent.Compaction.InTurn.IsEnabled() || absent.Compaction.InTurn.EffectiveKeepRecentSteps() != 4 {
		t.Fatalf("absent keys must mean the defaults: %+v", absent.Compaction.InTurn)
	}

	set, err := load("  in_turn:\n    enable: false\n    keep_recent_steps: 7\n")
	if err != nil {
		t.Fatal(err)
	}
	if set.Compaction.InTurn.IsEnabled() || set.Compaction.InTurn.EffectiveKeepRecentSteps() != 7 {
		t.Fatalf("explicit values lost: %+v", set.Compaction.InTurn)
	}

	if _, err := load("  in_turn:\n    keep_recent_steps: 0\n"); err == nil || !strings.Contains(err.Error(), "in_turn.keep_recent_steps") {
		t.Fatalf("keep_recent_steps: 0 must be refused at load, got %v", err)
	}
}
