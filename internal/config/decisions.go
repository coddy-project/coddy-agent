package config

import (
	"fmt"
	"strings"
)

// The models the NeuralDeep decisions endpoint serves (docs/features/decisions.md).
const (
	// DecisionsModelFRIDA is the encoder-only decisions model: a command text
	// of up to 512 tokens, roughly 20 ms per request.
	DecisionsModelFRIDA = "frida-decisions"
	// DecisionsModelClef is the generative decisions model: up to 8192 tokens
	// of command text together with the question schema, roughly 150 ms per
	// request.
	DecisionsModelClef = "clef-flash"
)

// DecisionsModels lists the models decisions.model accepts, in UI order.
var DecisionsModels = []string{DecisionsModelFRIDA, DecisionsModelClef}

// IsDecisionsModel says whether model is one the decisions endpoint serves.
func IsDecisionsModel(model string) bool {
	for _, m := range DecisionsModels {
		if model == m {
			return true
		}
	}
	return false
}

// DecisionsDefaultThreshold is the probability of the unsafe option at or
// above which a command is rejected when decisions.threshold names none.
const DecisionsDefaultThreshold = 0.5

// DecisionsConfig controls the command safety check that asks the NeuralDeep
// decisions endpoint about a shell command before it runs without a permission
// prompt (transport in internal/llm/decisions.go, the gate itself in
// internal/agent/decisions.go).
type DecisionsConfig struct {
	// Enabled turns the check on. Off by default: the check needs a NeuralDeep
	// credential, and an operator who has none should not have commands
	// rejected over it.
	Enabled bool `yaml:"enable"`

	// Model is the decisions model to ask. Empty uses DecisionsModelFRIDA.
	Model string `yaml:"model"`

	// Threshold is the probability of the unsafe option at or above which a
	// command is rejected. 0 uses the default (0.5); lower rejects more
	// aggressively, higher lets borderline commands through.
	Threshold float64 `yaml:"threshold"`
}

// Normalize trims string fields in place.
func (d *DecisionsConfig) Normalize() {
	d.Model = strings.TrimSpace(d.Model)
}

// ApplyDefaults fills the empty model with the default decisions model.
func (d *DecisionsConfig) ApplyDefaults() {
	if d.Model == "" {
		d.Model = DecisionsModelFRIDA
	}
}

// EffectiveModel returns model with the default applied, for readers that
// run on a config the loader did not default (tests, a hand-built Config).
func (d *DecisionsConfig) EffectiveModel() string {
	if d.Model == "" {
		return DecisionsModelFRIDA
	}
	return d.Model
}

// Validate rejects a model the decisions endpoint does not serve and a
// threshold outside 0..1 (0 uses the default).
func (d *DecisionsConfig) Validate() error {
	if d.Model != "" && !IsDecisionsModel(d.Model) {
		return fmt.Errorf("model %q is not a decisions model (one of %s)", d.Model, strings.Join(DecisionsModels, ", "))
	}
	if d.Threshold < 0 || d.Threshold > 1 {
		return fmt.Errorf("threshold %v is outside 0..1 (0 uses the default %v)", d.Threshold, DecisionsDefaultThreshold)
	}
	return nil
}

// EffectiveThreshold returns threshold with the default applied.
func (d *DecisionsConfig) EffectiveThreshold() float64 {
	if d.Threshold <= 0 {
		return DecisionsDefaultThreshold
	}
	return d.Threshold
}
