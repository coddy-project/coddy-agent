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
