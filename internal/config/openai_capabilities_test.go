package config_test

import (
	"encoding/json"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"reflect"
	"testing"
)

func TestOpenAIModelSpecificReasoning(t *testing.T) {
	for model, want := range map[string][]string{
		"api/gpt-6-astra":           {"low", "medium", "high", "xhigh", "max"},
		"api/gpt-6.1-sol":           {"low", "medium", "high", "xhigh", "max"},
		"gateway/openai/gpt-6-luna": {"none", "low", "medium", "high", "xhigh", "max"},
	} {
		m := config.ModelEntry{Model: model}
		if got := m.ResolvedReasoningLevels(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, want %v", model, got, want)
		}
	}
}

func TestOpenAIControlsRoundTrip(t *testing.T) {
	p := writeConfig(t, reasoningYAML("    protocol: responses\n    service_tier: ultrafast\n    reasoning_levels: []\n"))
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(config.ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	next := saveThroughSettings(t, p, body)
	m := next.Models[0]
	if m.Protocol != "responses" || m.ServiceTier != "ultrafast" || m.ReasoningLevels == nil || len(*m.ReasoningLevels) != 0 {
		t.Fatalf("round trip=%+v", m)
	}
}
