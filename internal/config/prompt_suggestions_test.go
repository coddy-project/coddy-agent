package config_test

import (
	"encoding/json"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"gopkg.in/yaml.v3"
)

func TestPromptSuggestionsConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want bool
	}{
		{"absent", "agent: {}", true},
		{"null", "agent: {prompt_suggestions: null}", true},
		{"enabled", "agent: {prompt_suggestions: true}", true},
		{"disabled", "agent: {prompt_suggestions: false}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg config.Config
			if err := yaml.Unmarshal([]byte(tc.yaml), &cfg); err != nil {
				t.Fatal(err)
			}
			if got := cfg.Agent.EffectivePromptSuggestions(); got != tc.want {
				t.Fatalf("enabled = %v, want %v", got, tc.want)
			}
			data, err := json.Marshal(config.ConfigToJSONDTO(&cfg))
			if err != nil {
				t.Fatal(err)
			}
			var dto config.ConfigJSON
			if err := json.Unmarshal(data, &dto); err != nil {
				t.Fatal(err)
			}
			restored := config.JSONDTOToConfig(&dto, config.Paths{})
			if got := restored.Agent.EffectivePromptSuggestions(); got != tc.want {
				t.Fatalf("JSON round trip enabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPromptSuggestionsSettingsDefault(t *testing.T) {
	root := config.UISchemaMap()["properties"].(map[string]interface{})
	agent := root["agent"].(map[string]interface{})["properties"].(map[string]interface{})
	field, ok := agent["prompt_suggestions"].(map[string]interface{})
	if !ok || field["type"] != "boolean" || field["default"] != true {
		t.Fatalf("settings must offer an enabled-by-default switch, got %#v", field)
	}
}

func TestPromptSuggestionsContextConfiguration(t *testing.T) {
	for _, mode := range []string{"", "recent", "full"} {
		t.Run("mode_"+mode, func(t *testing.T) {
			input := "agent: {}"
			if mode != "" {
				input = "agent: {prompt_suggestions_context: " + mode + "}"
			}
			var cfg config.Config
			if err := yaml.Unmarshal([]byte(input), &cfg); err != nil {
				t.Fatal(err)
			}
			if err := cfg.Agent.Validate(); err != nil {
				t.Fatal(err)
			}
			want := mode
			if want == "" {
				want = "recent"
			}
			if got := cfg.Agent.EffectivePromptSuggestionsContext(); got != want {
				t.Fatalf("context=%q, want %q", got, want)
			}
			data, err := json.Marshal(config.ConfigToJSONDTO(&cfg))
			if err != nil {
				t.Fatal(err)
			}
			var dto config.ConfigJSON
			if err := json.Unmarshal(data, &dto); err != nil {
				t.Fatal(err)
			}
			restored := config.JSONDTOToConfig(&dto, config.Paths{})
			if restored.Agent.PromptSuggestionsContext != mode {
				t.Fatal("context lost in JSON round trip")
			}
			encoded, err := yaml.Marshal(restored)
			if err != nil {
				t.Fatal(err)
			}
			var again config.Config
			if err := yaml.Unmarshal(encoded, &again); err != nil {
				t.Fatal(err)
			}
			if again.Agent.PromptSuggestionsContext != mode {
				t.Fatal("context lost in YAML round trip")
			}
		})
	}
	bad := config.Agent{PromptSuggestionsContext: "unknown"}
	if bad.Validate() == nil {
		t.Fatal("invalid suggestion context accepted")
	}
}
