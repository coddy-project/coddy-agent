package config

import "testing"

func TestDecisionsDefaults(t *testing.T) {
	cfg, err := parseValidateYAMLBytes("", Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Decisions.Enabled {
		t.Fatal("decisions must be disabled by default")
	}
	if got := cfg.Decisions.Model; got != DecisionsModelFRIDA {
		t.Fatalf("default model = %q, want %q", got, DecisionsModelFRIDA)
	}
}

func TestDecisionsParse(t *testing.T) {
	yaml := `
decisions:
  enable: true
  model: clef-flash
`
	cfg, err := parseValidateYAMLBytes(yaml, Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Decisions.Enabled {
		t.Fatal("enable: true ignored")
	}
	if got := cfg.Decisions.Model; got != DecisionsModelClef {
		t.Fatalf("model = %q, want %q", got, DecisionsModelClef)
	}
}

func TestDecisionsModelBlankFallsBackToDefault(t *testing.T) {
	cfg, err := parseValidateYAMLBytes("decisions:\n  enable: true\n  model: \"\"\n", Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Decisions.Model; got != DecisionsModelFRIDA {
		t.Fatalf("model = %q, want %q", got, DecisionsModelFRIDA)
	}
}

func TestDecisionsRejectsUnknownModel(t *testing.T) {
	_, err := parseValidateYAMLBytes("decisions:\n  model: gpt-4o\n", Paths{})
	if err == nil {
		t.Fatal("an unknown decisions model must fail validation")
	}
	want := "decisions:"
	if sub := string([]byte(err.Error())[:len(want)]); sub != want {
		t.Fatalf("error = %q, want prefix %q", err.Error(), want)
	}
}

func TestDecisionsValidateAcceptsServedModels(t *testing.T) {
	for _, m := range DecisionsModels {
		d := DecisionsConfig{Model: m}
		if err := d.Validate(); err != nil {
			t.Fatalf("model %q: %v", m, err)
		}
	}
	d := DecisionsConfig{}
	if err := d.Validate(); err != nil {
		t.Fatalf("empty model: %v", err)
	}
}

func TestDecisionsNormalizeTrimsModel(t *testing.T) {
	d := DecisionsConfig{Model: "  clef-flash\n"}
	d.Normalize()
	if d.Model != DecisionsModelClef {
		t.Fatalf("model = %q", d.Model)
	}
}

func TestDecisionsThresholdValidationAndDefault(t *testing.T) {
	cfg, err := parseValidateYAMLBytes("decisions:\n  threshold: 0.9\n", Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Decisions.EffectiveThreshold(); got != 0.9 {
		t.Fatalf("threshold = %v, want 0.9", got)
	}
	// 0 and unset both mean the default.
	cfg, err = parseValidateYAMLBytes("decisions:\n  threshold: 0\n", Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Decisions.EffectiveThreshold(); got != DecisionsDefaultThreshold {
		t.Fatalf("threshold 0 = %v, want the default %v", got, DecisionsDefaultThreshold)
	}
	cfg, err = parseValidateYAMLBytes("", Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Decisions.EffectiveThreshold(); got != DecisionsDefaultThreshold {
		t.Fatalf("unset threshold = %v, want %v", got, DecisionsDefaultThreshold)
	}
	for _, bad := range []string{"-0.1", "1.5"} {
		if _, err := parseValidateYAMLBytes("decisions:\n  threshold: "+bad+"\n", Paths{}); err == nil {
			t.Fatalf("threshold %s must fail validation", bad)
		}
	}
}

// The settings form takes its bounds from the UI schema it is served, so the
// threshold field there carries the same 0..1 range Validate enforces.
func TestDecisionsUISchemaBoundsTheThreshold(t *testing.T) {
	props, _ := UISchemaMap()["properties"].(map[string]interface{})
	decisions, _ := props["decisions"].(map[string]interface{})
	fields, _ := decisions["properties"].(map[string]interface{})
	threshold, _ := fields["threshold"].(map[string]interface{})
	if threshold == nil {
		t.Fatal("UI schema has no decisions.threshold")
	}
	if threshold["minimum"] != 0.0 || threshold["maximum"] != 1.0 {
		t.Fatalf("threshold bounds = %v..%v, want 0..1", threshold["minimum"], threshold["maximum"])
	}
}
