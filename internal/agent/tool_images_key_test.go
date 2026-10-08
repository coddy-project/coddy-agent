package agent

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// models[].multimodal has three states and the agent reads it through the
// config's one reader: an absent key and a written false both keep a picture
// from the model (and name the key in the refusal), a written true shows it, and
// a model the config does not list fails closed.
func TestModelReadsImagesFollowsTheMultimodalKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  *bool
		want bool
	}{
		{"absent", nil, false},
		{"written false", config.BoolPtr(false), false},
		{"written true", config.BoolPtr(true), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
				Models:    []config.ModelEntry{{Model: "fake/eyes", MaxTokens: 100, Multimodal: tc.key}},
				Agent:     config.Agent{Model: "fake/eyes"},
			}
			st := &session.State{ID: "sess_key_" + strings.ReplaceAll(tc.name, " ", "_"), CWD: t.TempDir(), Mode: session.ModeAgent}
			ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
			if got := ag.modelReadsImages(); got != tc.want {
				t.Fatalf("modelReadsImages = %v, want %v", got, tc.want)
			}
			err := ag.toolImageRefusal()
			if tc.want && err != nil {
				t.Fatalf("a model that reads images was refused: %v", err)
			}
			if !tc.want && (err == nil || !strings.Contains(err.Error(), "models[].multimodal")) {
				t.Fatalf("refusal = %v, want one that names models[].multimodal", err)
			}
		})
	}

	t.Run("unknown model", func(t *testing.T) {
		cfg := &config.Config{
			Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
			Agent:     config.Agent{Model: "fake/eyes"},
		}
		st := &session.State{ID: "sess_key_unknown", CWD: t.TempDir(), Mode: session.ModeAgent}
		ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
		if ag.modelReadsImages() {
			t.Fatal("a model the config does not list must fail closed")
		}
	})
}
