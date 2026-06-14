package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestUISchemaOmitsHTTPServerFromUI(t *testing.T) {
	doc := config.UISchemaMap()
	props, ok := doc["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("properties")
	}
	if _, ok := props["httpserver"]; ok {
		t.Fatal("httpserver must not be exposed in UI schema")
	}
}

func TestUISchemaRootPropertyOrder(t *testing.T) {
	doc := config.UISchemaMap()
	ord, ok := doc["x-coddy-property-order"].([]interface{})
	if !ok || len(ord) < 3 {
		t.Fatalf("x-coddy-property-order: %v", doc["x-coddy-property-order"])
	}
	if ord[0] != "providers" {
		t.Fatalf("first key %v", ord[0])
	}
}

func TestUISchemaProviderNamePatternAndAPIKeyPlaceholderHint(t *testing.T) {
	doc := config.UISchemaMap()
	providers := doc["properties"].(map[string]interface{})["providers"].(map[string]interface{})
	items := providers["items"].(map[string]interface{})
	pprops := items["properties"].(map[string]interface{})
	name := pprops["name"].(map[string]interface{})
	if got, want := name["pattern"], `^[a-zA-Z][a-zA-Z0-9_-]*$`; got != want {
		t.Fatalf("provider name pattern: got %v want %v", got, want)
	}
	apiKey := pprops["api_key"].(map[string]interface{})
	if apiKey["x-coddy-provider-api-key-env-placeholder"] != true {
		t.Fatal("expected x-coddy-provider-api-key-env-placeholder on api_key")
	}
}

func TestUISchemaAgentFieldHasDescription(t *testing.T) {
	doc := config.UISchemaMap()
	props := doc["properties"].(map[string]interface{})
	agent := props["agent"].(map[string]interface{})
	ap := agent["properties"].(map[string]interface{})
	model := ap["model"].(map[string]interface{})
	if model["description"] == nil || model["description"] == "" {
		t.Fatal("expected model description")
	}
	if model["default"] == nil {
		t.Fatal("expected model default from schema example")
	}
}

func TestUISchemaModelHasReasoningFields(t *testing.T) {
	doc := config.UISchemaMap()
	models := doc["properties"].(map[string]interface{})["models"].(map[string]interface{})
	items := models["items"].(map[string]interface{})
	mprops := items["properties"].(map[string]interface{})
	rl, ok := mprops["reasoning_levels"].(map[string]interface{})
	if !ok {
		t.Fatal("expected reasoning_levels in model schema")
	}
	if rl["type"] != "array" {
		t.Fatalf("reasoning_levels type %v want array", rl["type"])
	}
	if _, ok := mprops["reasoning_default"].(map[string]interface{}); !ok {
		t.Fatal("expected reasoning_default in model schema")
	}
}

func TestConfigJSONRoundTripAndYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCODDYHome, home)
	yml := `
providers:
  - name: openai
    type: openai
    api_key: "k"

models:
  - model: "openai/gpt-4o"
    max_tokens: 4096
    temperature: 0.1

agent:
  model: "openai/gpt-4o"
`
	p := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(p, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	dto := config.ConfigToJSONDTO(cfg)
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	paths := cfg.Paths
	cfg2, err := config.ParseAndValidateConfigJSON(raw, paths)
	if err != nil {
		t.Fatalf("ParseAndValidateConfigJSON: %v", err)
	}
	if cfg2.Agent.Model != "openai/gpt-4o" {
		t.Fatalf("model %q", cfg2.Agent.Model)
	}
	yb, err := config.MarshalConfigYAML(cfg2)
	if err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(home, "out.yaml")
	if err := os.WriteFile(outPath, yb, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg3, err := config.Load(outPath)
	if err != nil {
		t.Fatalf("reload yaml: %v", err)
	}
	if cfg3.Agent.Model != "openai/gpt-4o" {
		t.Fatalf("yaml round-trip model %q", cfg3.Agent.Model)
	}
}
