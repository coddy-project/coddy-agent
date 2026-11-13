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
