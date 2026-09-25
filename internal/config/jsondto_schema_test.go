package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestQueueModeJSONRoundTripAndValidation(t *testing.T) {
	original := &config.Config{Agent: config.Agent{QueueMode: "after_turn"}}
	got := config.JSONDTOToConfig(config.ConfigToJSONDTO(original), config.Paths{})
	if got.Agent.QueueMode != "after_turn" { t.Fatalf("queue mode = %q", got.Agent.QueueMode) }
	if err := got.Agent.Validate(); err != nil { t.Fatal(err) }
	got.Agent.QueueMode = "unknown"
	if err := got.Agent.Validate(); err == nil { t.Fatal("invalid queue mode accepted") }
}

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
	// Context compaction is a tab of its own, right after the ReAct agent tab:
	// the same loop, and the settings an operator reads together.
	at := func(key string) int {
		for i, v := range ord {
			if v == key {
				return i
			}
		}
		return -1
	}
	agent, compaction := at("agent"), at("compaction")
	if agent < 0 || compaction != agent+1 {
		t.Fatalf("compaction must follow agent: agent=%d compaction=%d order=%v", agent, compaction, ord)
	}
	// The tabs read in groups of meaning: the models, the loop that runs them,
	// what the agent can do, what runs it unattended, and the operation of
	// the process last.
	want := []interface{}{
		"providers", "models",
		"agent", "compaction", "memory",
		"tools", "mcp_servers", "skills", "subagents", "hooks",
		"scheduler", "gateways",
		"logger", "sessions", "prompts", "instructions",
	}
	if !reflect.DeepEqual(ord, want) {
		t.Fatalf("root order:\n got %v\nwant %v", ord, want)
	}
}

func TestUISchemaProviderNamePatternAndAPIKeyPlaceholderHint(t *testing.T) {
	doc := config.UISchemaMap()
	providers := doc["properties"].(map[string]interface{})["providers"].(map[string]interface{})
	items := providers["items"].(map[string]interface{})
	pprops := items["properties"].(map[string]interface{})
	name := pprops["name"].(map[string]interface{})
	if got, want := name["pattern"], `^[a-zA-Z][a-zA-Z0-9_\-]*$`; got != want {
		t.Fatalf("provider name pattern: got %v want %v", got, want)
	}
	apiKey := pprops["api_key"].(map[string]interface{})
	if apiKey["x-coddy-provider-api-key-env-placeholder"] != true {
		t.Fatal("expected x-coddy-provider-api-key-env-placeholder on api_key")
	}
}

func TestUISchemaProviderTypeEnumMatchesConfig(t *testing.T) {
	doc := config.UISchemaMap()
	providers := doc["properties"].(map[string]interface{})["providers"].(map[string]interface{})
	items := providers["items"].(map[string]interface{})
	pprops := items["properties"].(map[string]interface{})
	typeProp := pprops["type"].(map[string]interface{})
	raw, ok := typeProp["enum"].([]string)
	if !ok {
		t.Fatalf("providers[].type enum = %#v", typeProp["enum"])
	}
	got := make(map[string]struct{}, len(raw))
	for _, v := range raw {
		got[v] = struct{}{}
	}
	if len(got) != len(config.AllowedLLMProviderTypes) {
		t.Fatalf("providers[].type enum = %v, want keys of %v", raw, config.AllowedLLMProviderTypes)
	}
	for want := range config.AllowedLLMProviderTypes {
		if _, ok := got[want]; !ok {
			t.Fatalf("providers[].type enum = %v, missing %q", raw, want)
		}
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

skills:
  sources:
    - owner/repo
    - https://example.com/marketplace.json
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
	// skills.sources must survive the JSON DTO round-trip so the config-form
	// UI can view and persist remote marketplace sources (not just skills.dirs).
	if len(cfg2.Skills.Sources) != 2 || cfg2.Skills.Sources[0] != "owner/repo" ||
		cfg2.Skills.Sources[1] != "https://example.com/marketplace.json" {
		t.Fatalf("skills.sources lost in JSON round-trip: %v", cfg2.Skills.Sources)
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
	if len(cfg3.Skills.Sources) != 2 {
		t.Fatalf("skills.sources lost in yaml round-trip: %v", cfg3.Skills.Sources)
	}
}

func TestUISchemaOmitsMCPPolicyFromUI(t *testing.T) {
	// mcp.project_trust is edited in the MCP servers tab, next to the servers
	// it governs, so it must not become a settings section of its own.
	doc := config.UISchemaMap()
	props, ok := doc["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("properties")
	}
	if _, ok := props["mcp"]; ok {
		t.Fatal("mcp must not be exposed as its own UI section")
	}
	// Hiding it must not drop it from the saved document.
	cfg := &config.Config{MCP: config.MCP{ProjectTrust: config.ProjectTrustAllow}}
	back := config.JSONDTOToConfig(config.ConfigToJSONDTO(cfg), config.Paths{})
	if got := back.MCP.ResolvedProjectTrust(); got != config.ProjectTrustAllow {
		t.Fatalf("project_trust after round trip = %q, want %q", got, config.ProjectTrustAllow)
	}
}

// The Settings form round-trips the whole config through the JSON DTO; a
// section that is not on the DTO is silently reset by any unrelated save.
func TestPreviewServerSurvivesTheJSONDTO(t *testing.T) {
	off := false
	cfg := &config.Config{}
	cfg.Tools.PreviewServer = config.ToolPreviewServer{Enabled: &off, Host: "0.0.0.0", PublicHost: "dev.example"}

	raw, err := json.Marshal(config.ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"preview_server"`) {
		t.Fatalf("GET DTO dropped tools.preview_server: %s", raw)
	}

	put := `{"tools":{"preview_server":{"enable":false,"host":"0.0.0.0","public_host":"dev.example"}}}`
	var j config.ConfigJSON
	if err := json.Unmarshal([]byte(put), &j); err != nil {
		t.Fatal(err)
	}
	back := config.JSONDTOToConfig(&j, config.Paths{})
	got := back.Tools.PreviewServer
	if got.Enabled == nil || *got.Enabled || got.Host != "0.0.0.0" || got.PublicHost != "dev.example" {
		t.Fatalf("PUT path dropped tools.preview_server: %+v", got)
	}
}

// rules, ui and the two fallback_models lists used to be missing from ConfigJSON,
// so any unrelated settings save silently reset them (issue #265 companion).
func TestRulesUIAndFallbackModelsSurviveTheJSONDTO(t *testing.T) {
	off := false
	cfg := &config.Config{}
	cfg.Rules = config.Rules{AutoDiscover: &off, Systems: []string{"acme/rules"}}
	cfg.UI.Enabled = &off
	cfg.Compaction.FallbackModels = []string{"codex/gpt-5.5", "neuraldeep/gpt-oss-120b"}
	cfg.Memory.FallbackModels = []string{"codex/gpt-5.5"}

	raw, err := json.Marshal(config.ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"rules"`, `"ui"`, `"auto_discover":false`, `"enable":false`, `"fallback_models"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("GET DTO dropped %s: %s", want, raw)
		}
	}

	back := config.JSONDTOToConfig(config.ConfigToJSONDTO(cfg), config.Paths{})
	if back.Rules.AutoDiscover == nil || *back.Rules.AutoDiscover {
		t.Fatalf("rules.auto_discover lost: %+v", back.Rules)
	}
	if len(back.Rules.Systems) != 1 || back.Rules.Systems[0] != "acme/rules" {
		t.Fatalf("rules.systems lost: %+v", back.Rules)
	}
	if back.UI.Enabled == nil || *back.UI.Enabled {
		t.Fatalf("ui.enable lost: %+v", back.UI)
	}
	if len(back.Compaction.FallbackModels) != 2 || back.Compaction.FallbackModels[1] != "neuraldeep/gpt-oss-120b" {
		t.Fatalf("compaction.fallback_models lost: %v", back.Compaction.FallbackModels)
	}
	if len(back.Memory.FallbackModels) != 1 || back.Memory.FallbackModels[0] != "codex/gpt-5.5" {
		t.Fatalf("memory.fallback_models lost: %v", back.Memory.FallbackModels)
	}
}

// Every yaml-tagged field of Config must have a ConfigJSON counterpart, or a
// settings save drops it. rules and ui went missing once already at the top
// level, and compaction.fallback_models / memory.fallback_models one level
// deeper; the walk is recursive so a field cannot silently join them at any
// depth.
func TestConfigJSONCoversEveryConfigSection(t *testing.T) {
	assertJSONCoversYAML(t, reflect.TypeOf(config.Config{}), reflect.TypeOf(config.ConfigJSON{}), "Config")
}

func assertJSONCoversYAML(t *testing.T, cfgT, dtoT reflect.Type, prefix string) {
	t.Helper()
	jsonFields := map[string]reflect.Type{}
	for i := 0; i < dtoT.NumField(); i++ {
		f := dtoT.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != "" && name != "-" {
			jsonFields[name] = f.Type
		}
	}
	for i := 0; i < cfgT.NumField(); i++ {
		f := cfgT.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		jt, ok := jsonFields[name]
		if !ok {
			t.Errorf("%s.%s (yaml %q) has no ConfigJSON field - a settings save would drop it", prefix, f.Name, name)
			continue
		}
		if ct, jt := taggedStruct(f.Type, "yaml"), taggedStruct(jt, "json"); ct != nil && jt != nil {
			assertJSONCoversYAML(t, ct, jt, prefix+"."+name)
		}
	}
}

// taggedStruct unwraps pointers, slices, arrays and maps down to a struct type,
// and returns it only when it carries fields tagged with the given tag - a
// ConfigJSON mirror struct. Leaf structs like time.Time have none and stay nil.
func taggedStruct(t reflect.Type, tag string) reflect.Type {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).Tag.Get(tag) != "" {
			return t
		}
	}
	return nil
}
