//go:build http

package httpserver

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// configuredModelMultimodal answers from models[].multimodal through the
// config's one reader: only a written true opts in, an absent key, a written
// false, an unknown model and a missing config all fail closed.
func TestConfiguredModelMultimodalReadsTheKeyThroughTheConfig(t *testing.T) {
	cfg := &config.Config{Models: []config.ModelEntry{
		{Model: "openai/absent"},
		{Model: "openai/no", Multimodal: config.BoolPtr(false)},
		{Model: "openai/yes", Multimodal: config.BoolPtr(true)},
	}}
	for model, want := range map[string]bool{
		"openai/absent":  false,
		"openai/no":      false,
		"openai/yes":     true,
		"openai/missing": false,
	} {
		if got := configuredModelMultimodal(cfg, model); got != want {
			t.Errorf("%s: configuredModelMultimodal = %v, want %v", model, got, want)
		}
	}
	if configuredModelMultimodal(nil, "openai/yes") {
		t.Error("a missing config reads no images")
	}
}
