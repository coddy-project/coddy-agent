package config_test

import (
	"bytes"
	"encoding/json"
	"flag"
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
	// The tabs read in groups of meaning: the models and the toolsets they
	// name, the loop that runs them, what the agent can do, what runs it
	// unattended, and the operation of the process last.
	want := []interface{}{
		"providers", "models", "toolsets",
		"agent", "supervisor", "compaction", "memory", "decisions",
		"tools", "skills", "subagents", "hooks",
		"scheduler", "gateways",
		"logger", "metrics", "sessions", "prompts", "instructions",
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

// The Settings form is drawn from UISchemaMap: compaction has no in_turn
// section (the fold of the turn and the recovery are part of auto_enable), and
// result_eviction offers only the read/grep knobs (the listing tools and their
// step window are fixed in the agent, so the form has no control for them).
func TestUISchemaCompactionOffersNoInTurnSection(t *testing.T) {
	doc := config.UISchemaMap()
	compaction := doc["properties"].(map[string]interface{})["compaction"].(map[string]interface{})
	props := compaction["properties"].(map[string]interface{})

	order := func(node map[string]interface{}) []string {
		var got []string
		raw, _ := node["x-coddy-property-order"].([]interface{})
		for _, v := range raw {
			got = append(got, v.(string))
		}
		return got
	}

	if _, ok := props["in_turn"]; ok {
		t.Fatalf("compaction.in_turn = %#v, want no such section", props["in_turn"])
	}
	if got, want := order(compaction), []string{"enable", "auto_enable", "threshold_percent", "keep_recent_turns", "model", "result_eviction"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("compaction field order %v, want %v", got, want)
	}

	re := props["result_eviction"].(map[string]interface{})
	reFields := re["properties"].(map[string]interface{})
	want := []string{"enable", "keep_recent", "min_result_bytes", "start_percent"}
	if got := order(re); !reflect.DeepEqual(got, want) || len(reFields) != len(want) {
		t.Fatalf("result_eviction fields %v (order %v), want %v", reFields, got, want)
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

var updateTestdata = flag.Bool("update", false, "rewrite testdata/ui_schema*.json from the builders")

// The web UI's tests read checked copies of the schemas the settings form is
// served (external/ui/src/ui/settings/settingsServer.fakes.ts), so a schema
// edit that leaves them behind fails here. Regenerate them with
//
//	go test ./internal/config -run '^TestUISchemaTestdataIsCurrent$' -update
func TestUISchemaTestdataIsCurrent(t *testing.T) {
	for _, tc := range []struct {
		file  string
		build func() ([]byte, error)
	}{
		{"ui_schema.json", config.UISchemaJSON},
		{"ui_schema_relay.json", config.RelayUISchemaJSON},
	} {
		t.Run(tc.file, func(t *testing.T) {
			compact, err := tc.build()
			if err != nil {
				t.Fatal(err)
			}
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, compact, "", "  "); err != nil {
				t.Fatal(err)
			}
			pretty.WriteByte('\n')
			path := filepath.Join("testdata", tc.file)
			if *updateTestdata {
				if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v\nregenerate with: go test ./internal/config -run '^TestUISchemaTestdataIsCurrent$' -update", err)
			}
			got := strings.ReplaceAll(string(raw), "\r\n", "\n")
			if got != pretty.String() {
				t.Fatalf("%s is stale; regenerate with: go test ./internal/config -run '^TestUISchemaTestdataIsCurrent$' -update", path)
			}
		})
	}
}

// The metrics token is the bearer credential of a scrape, so the settings
// document reports only that one is set, and a save that sends none back keeps
// the stored one.
func TestMetricsSectionSurvivesTheJSONDTO(t *testing.T) {
	live := &config.Config{Metrics: config.MetricsConfig{Enable: true, Host: "0.0.0.0", Port: 9100, Token: "scrape-secret"}}
	raw, err := json.Marshal(config.ConfigToJSONDTO(live))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	section := doc["metrics"]
	if _, leaked := section["token"]; leaked || strings.Contains(string(raw), "scrape-secret") {
		t.Fatalf("the document carries the token: %s", raw)
	}
	if section["token_configured"] != true || section["enable"] != true || section["host"] != "0.0.0.0" || section["port"] != float64(9100) {
		t.Fatalf("metrics section = %v", section)
	}

	kept, err := config.ParseConfigJSONPreservingSecrets(raw, config.Paths{}, live)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Metrics != live.Metrics {
		t.Fatalf("a save without a token changed the section: %+v, want %+v", kept.Metrics, live.Metrics)
	}
	dropped, err := config.ParseAndValidateConfigJSON(raw, config.Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if dropped.Metrics.Token != "" {
		t.Fatalf("a save with nothing to keep it from invented the token %q", dropped.Metrics.Token)
	}
	replaced, err := config.ParseConfigJSONPreservingSecrets([]byte(`{"metrics":{"enable":true,"token":"new-secret"}}`), config.Paths{}, live)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Metrics.Token != "new-secret" {
		t.Fatalf("a new token was not taken: %q", replaced.Metrics.Token)
	}
}

// The Metrics tab edits the four keys, with the token as a write-only field.
func TestUISchemaMetricsSection(t *testing.T) {
	doc := config.UISchemaMap()
	section, ok := doc["properties"].(map[string]interface{})["metrics"].(map[string]interface{})
	if !ok {
		t.Fatal("the UI schema has no metrics section")
	}
	if order := section["x-coddy-property-order"]; !reflect.DeepEqual(order, []interface{}{"enable", "host", "port", "token"}) {
		t.Fatalf("metrics property order = %v", order)
	}
	token := section["properties"].(map[string]interface{})["token"].(map[string]interface{})
	if token["writeOnly"] != true || token["x-coddy-configured"] != "token_configured" {
		t.Fatalf("metrics.token is not a write-only field: %v", token)
	}
}

// A binary that cannot run the exporter offers no Metrics tab. The section stays
// in the settings document, so a save from that form still writes it back.
func TestUISchemaJSONHidingOmitsSection(t *testing.T) {
	raw, err := config.UISchemaJSONHiding("metrics")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	props := doc["properties"].(map[string]interface{})
	if _, shown := props["metrics"]; shown {
		t.Fatal("the hidden section is still among the properties")
	}
	if _, kept := props["logger"]; !kept {
		t.Fatal("hiding metrics dropped another section")
	}
	for _, key := range doc["x-coddy-property-order"].([]interface{}) {
		if key == "metrics" {
			t.Fatalf("the hidden section is still in the tab order: %v", doc["x-coddy-property-order"])
		}
	}

	whole, err := config.UISchemaJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(whole, []byte(`"metrics"`)) {
		t.Fatal("the full schema lost the section")
	}
	none, err := config.UISchemaJSONHiding()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(none, whole) {
		t.Fatal("hiding nothing must give the full schema")
	}
}
