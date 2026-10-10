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
	if got.Agent.QueueMode != "after_turn" {
		t.Fatalf("queue mode = %q", got.Agent.QueueMode)
	}
	if err := got.Agent.Validate(); err != nil {
		t.Fatal(err)
	}
	got.Agent.QueueMode = "unknown"
	if err := got.Agent.Validate(); err == nil {
		t.Fatal("invalid queue mode accepted")
	}
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
	// The supervisor follows the ReAct loop; compaction stays beside both.
	at := func(key string) int {
		for i, v := range ord {
			if v == key {
				return i
			}
		}
		return -1
	}
	agent, supervisor, compaction := at("agent"), at("supervisor"), at("compaction")
	if agent < 0 || supervisor != agent+1 || compaction != supervisor+1 {
		t.Fatalf("supervisor and compaction must follow agent: order=%v", ord)
	}
	// The tabs read in groups of meaning: the models, the loop that runs them,
	// what the agent can do, what runs it unattended, and the operation of
	// the process last.
	want := []interface{}{
		"providers", "models",
		"agent", "supervisor", "compaction", "memory",
		"tools", "skills", "subagents", "hooks",
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
	off, ok := mprops["allow_reasoning_off"].(map[string]interface{})
	if !ok {
		t.Fatal("expected allow_reasoning_off in model schema")
	}
	if off["type"] != "boolean" || off["default"] != false {
		t.Fatalf("allow_reasoning_off schema = %#v, want boolean default false", off)
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
  project_trust: allow
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
	// skills.project_trust must survive the JSON DTO round-trip so the
	// settings form can view and persist it (not just skills.dirs).
	if cfg2.Skills.ResolvedProjectTrust() != config.ProjectTrustAllow {
		t.Fatalf("skills.project_trust lost in JSON round-trip: %q", cfg2.Skills.ProjectTrust)
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
	if cfg3.Skills.ResolvedProjectTrust() != config.ProjectTrustAllow {
		t.Fatalf("skills.project_trust lost in yaml round-trip: %q", cfg3.Skills.ProjectTrust)
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
	cfg.Compaction.AutoEnabled = &off
	cfg.Memory.FallbackModels = []string{"codex/gpt-5.5"}

	raw, err := json.Marshal(config.ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"rules"`, `"ui"`, `"auto_discover":false`, `"enable":false`, `"auto_enable":false`, `"fallback_models"`} {
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
	if back.Compaction.AutoEnabled == nil || *back.Compaction.AutoEnabled {
		t.Fatalf("compaction.auto_enable lost: %+v", back.Compaction)
	}
	if len(back.Memory.FallbackModels) != 1 || back.Memory.FallbackModels[0] != "codex/gpt-5.5" {
		t.Fatalf("memory.fallback_models lost: %v", back.Memory.FallbackModels)
	}
}

// The Settings form is drawn from UISchemaMap: the eviction section offers the
// step window as a number and no longer calls itself read/grep only. The list of
// listing tools is not on the form: an absent list (all four tools) and an empty
// one (none) would be drawn alike, so the key stays in config.yaml until the form
// has a control that tells them apart.
func TestUISchemaResultEvictionOffersTheStepWindow(t *testing.T) {
	doc := config.UISchemaMap()
	compaction := doc["properties"].(map[string]interface{})["compaction"].(map[string]interface{})
	re := compaction["properties"].(map[string]interface{})["result_eviction"].(map[string]interface{})
	props := re["properties"].(map[string]interface{})

	if _, ok := props["tools"]; ok {
		t.Fatal("tools must stay off the form: an empty list there reads as the default but means none")
	}
	if steps, ok := props["keep_recent_steps"].(map[string]interface{}); !ok || steps["type"] != "integer" {
		t.Fatalf("keep_recent_steps = %#v, want an integer field", props["keep_recent_steps"])
	}

	order, _ := re["x-coddy-property-order"].([]interface{})
	var got []string
	for _, v := range order {
		got = append(got, v.(string))
	}
	want := []string{"enable", "keep_recent", "keep_recent_steps", "min_result_bytes", "start_percent"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("result_eviction field order %v, want %v", got, want)
	}
	if strings.Contains(re["title"].(string), "Read/grep") || strings.Contains(re["description"].(string), "read/grep") {
		t.Fatalf("the section still describes itself as read/grep only: %q / %q", re["title"], re["description"])
	}
}

// compaction.result_eviction.tools and keep_recent_steps ride the Settings
// round trip like every other key, and tools keeps the difference between an
// absent key (the default listing tools) and an explicit [] (read and grep only)
// exactly as models[].reasoning_levels does.
func TestResultEvictionListingKeysSurviveTheJSONDTO(t *testing.T) {
	roundTrip := func(t *testing.T, re config.ResultEviction) (string, config.ResultEviction) {
		t.Helper()
		cfg := &config.Config{}
		cfg.Compaction.ResultEviction = re
		raw, err := json.Marshal(config.ConfigToJSONDTO(cfg))
		if err != nil {
			t.Fatal(err)
		}
		var j config.ConfigJSON
		if err := json.Unmarshal(raw, &j); err != nil {
			t.Fatal(err)
		}
		return string(raw), config.JSONDTOToConfig(&j, config.Paths{}).Compaction.ResultEviction
	}

	t.Run("unset keys stay unset", func(t *testing.T) {
		raw, back := roundTrip(t, config.ResultEviction{})
		var doc struct {
			Compaction struct {
				ResultEviction map[string]json.RawMessage `json:"result_eviction"`
			} `json:"compaction"`
		}
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"tools", "keep_recent_steps"} {
			if _, ok := doc.Compaction.ResultEviction[key]; ok {
				t.Fatalf("an unset %s was written: %s", key, raw)
			}
		}
		if back.Tools != nil || back.KeepRecentSteps != nil {
			t.Fatalf("unset keys came back set: %+v", back)
		}
	})

	t.Run("an explicit empty list and an explicit zero are kept", func(t *testing.T) {
		empty := []string{}
		zero := 0
		raw, back := roundTrip(t, config.ResultEviction{Tools: &empty, KeepRecentSteps: &zero})
		if !strings.Contains(raw, `"tools":[]`) || !strings.Contains(raw, `"keep_recent_steps":0`) {
			t.Fatalf("GET DTO dropped the explicit values: %s", raw)
		}
		if back.Tools == nil || len(*back.Tools) != 0 {
			t.Fatalf("PUT path turned tools: [] into %v", back.Tools)
		}
		if back.KeepRecentSteps == nil || *back.KeepRecentSteps != 0 {
			t.Fatalf("PUT path turned keep_recent_steps: 0 into %v", back.KeepRecentSteps)
		}
	})

	t.Run("a list and a count are kept", func(t *testing.T) {
		tools := []string{"glob", "webfetch"}
		steps := 5
		_, back := roundTrip(t, config.ResultEviction{Tools: &tools, KeepRecentSteps: &steps})
		if back.Tools == nil || !reflect.DeepEqual(*back.Tools, tools) {
			t.Fatalf("tools lost: %v", back.Tools)
		}
		if back.KeepRecentSteps == nil || *back.KeepRecentSteps != 5 {
			t.Fatalf("keep_recent_steps lost: %v", back.KeepRecentSteps)
		}
		// The copy must not alias the source.
		(*back.Tools)[0] = "tampered"
		if tools[0] != "glob" {
			t.Fatalf("the DTO conversion shares the tools list with its source: %v", tools)
		}
	})
}

// The same through the whole Settings save - GET document, PUT body, YAML on
// disk, reload - which is where reasoning_levels once lost its nil / empty
// distinction.
func TestResultEvictionListingKeysSurviveASettingsSave(t *testing.T) {
	const head = `providers:
  - name: valera
    type: openai
    api_base: "http://127.0.0.1:9/v1"
    api_key: "k"
models:
  - model: "valera/m"
    max_tokens: 4096
agent:
  model: "valera/m"
compaction:
  result_eviction:
    keep_recent: 1
`
	save := func(t *testing.T, extra string) (*config.Config, string) {
		t.Helper()
		p := writeConfig(t, head+extra)
		cur, err := config.Load(p)
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(config.ConfigToJSONDTO(cur))
		if err != nil {
			t.Fatal(err)
		}
		reloaded := saveThroughSettings(t, p, body)
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return reloaded, string(raw)
	}

	t.Run("absent keys stay absent", func(t *testing.T) {
		cfg, raw := save(t, "")
		re := cfg.Compaction.ResultEviction
		if re.Tools != nil || re.KeepRecentSteps != nil {
			t.Fatalf("a save invented the keys: tools=%v steps=%v", re.Tools, re.KeepRecentSteps)
		}
		if strings.Contains(raw, "keep_recent_steps") || strings.Contains(raw, "tools:") {
			t.Fatalf("saved YAML must omit the unset keys:\n%s", raw)
		}
		if got := re.EffectiveTools(); len(got) != 4 {
			t.Fatalf("the default listing tools were lost: %v", got)
		}
	})

	t.Run("an explicit empty list and zero steps survive", func(t *testing.T) {
		cfg, raw := save(t, "    tools: []\n    keep_recent_steps: 0\n")
		re := cfg.Compaction.ResultEviction
		if re.Tools == nil || len(*re.Tools) != 0 {
			t.Fatalf("tools: [] was lost in the round trip: %v\n%s", re.Tools, raw)
		}
		if re.KeepRecentSteps == nil || *re.KeepRecentSteps != 0 {
			t.Fatalf("keep_recent_steps: 0 was lost in the round trip: %v\n%s", re.KeepRecentSteps, raw)
		}
		if len(re.EffectiveTools()) != 0 {
			t.Fatalf("an explicit empty list flipped back to the default: %v", re.EffectiveTools())
		}
	})

	t.Run("a list survives", func(t *testing.T) {
		cfg, _ := save(t, "    tools: [print_tree, webfetch]\n    keep_recent_steps: 5\n")
		re := cfg.Compaction.ResultEviction
		if !reflect.DeepEqual(re.EffectiveTools(), []string{"print_tree", "webfetch"}) || re.EffectiveKeepRecentSteps() != 5 {
			t.Fatalf("the configured values were lost: tools=%v steps=%d", re.EffectiveTools(), re.EffectiveKeepRecentSteps())
		}
	})
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

// The settings form edits tools.http_request.default_headers as rows of a name
// and a value: the UI schema has to say it is a map of strings, not a section
// with keys of its own, and place it after the allowlist.
func TestUISchemaDescribesDefaultHeadersAsAStringMap(t *testing.T) {
	doc := config.UISchemaMap()
	tools := doc["properties"].(map[string]interface{})["tools"].(map[string]interface{})
	section := tools["properties"].(map[string]interface{})["http_request"].(map[string]interface{})
	field, ok := section["properties"].(map[string]interface{})["default_headers"].(map[string]interface{})
	if !ok {
		t.Fatalf("tools.http_request has no default_headers field: %#v", section["properties"])
	}
	if field["type"] != "object" {
		t.Errorf("type = %v, want object", field["type"])
	}
	if _, fixed := field["properties"]; fixed {
		t.Error("a map of headers has no fixed properties")
	}
	values, ok := field["additionalProperties"].(map[string]interface{})
	if !ok || values["type"] != "string" {
		t.Errorf("additionalProperties = %#v, want a string schema", field["additionalProperties"])
	}
	if title, _ := field["title"].(string); title == "" {
		t.Error("the field has no title")
	}
	order, _ := section["x-coddy-property-order"].([]interface{})
	if len(order) != 2 || order[0] != "allowlist" || order[1] != "default_headers" {
		t.Errorf("property order = %v", order)
	}
}
