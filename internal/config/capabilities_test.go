package config_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// tri is a key of a model row that is absent, written true or written false.
type tri int

const (
	triAbsent tri = iota
	triTrue
	triFalse
)

func (k tri) String() string { return [...]string{"absent", "true", "false"}[k] }

var allTri = []tri{triAbsent, triTrue, triFalse}

// A row keeps multimodal as a plain boolean, so an absent key and a written
// false are one value; allow_reasoning_off keeps the written false apart from
// the absent key (AllowReasoningOffExplicit), and only a decoded document
// records it.

// triRow returns m with multimodal and allow_reasoning_off in the given states.
// A written false of allow_reasoning_off goes through YAML, the way a loaded
// row gets it.
func triRow(t testing.TB, m config.ModelEntry, mm, off tri) config.ModelEntry {
	t.Helper()
	m.Multimodal = mm == triTrue
	m.AllowReasoningOff = off == triTrue
	if off != triFalse {
		return m
	}
	var node yaml.Node
	if err := node.Encode(m); err != nil {
		t.Fatal(err)
	}
	// The key as a document carries it, whatever the writer emits for the row:
	// the decode then records it as written.
	val := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"}
	found := false
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "allow_reasoning_off" {
			node.Content[i+1], found = val, true
		}
	}
	if !found {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "allow_reasoning_off"}, val)
	}
	var out config.ModelEntry
	if err := node.Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.AllowReasoningOffExplicit() || out.AllowReasoningOff {
		t.Fatalf("the decoded row does not hold a written false: %+v", out)
	}
	return out
}

// mmState and offState are the states a row holds for the two keys.
func mmState(e *config.ModelEntry) tri {
	if e.Multimodal {
		return triTrue
	}
	return triAbsent
}

func offState(e *config.ModelEntry) tri {
	switch {
	case !e.AllowReasoningOffExplicit():
		return triAbsent
	case e.AllowReasoningOff:
		return triTrue
	default:
		return triFalse
	}
}

// mmWant is the multimodal state a row ends up with for a key written in
// state k: a written false is the absent key.
func mmWant(k tri) tri {
	if k == triFalse {
		return triAbsent
	}
	return k
}

func TestModelMultimodalIsNilSafeAndReadsTheKey(t *testing.T) {
	var nilCfg *config.Config
	if nilCfg.ModelMultimodal(&config.ModelEntry{Model: "a/b", Multimodal: true}) != true {
		t.Error("a nil config must still answer from the key written on the row")
	}
	cfg := &config.Config{}
	if cfg.ModelMultimodal(nil) {
		t.Error("a missing row reads no images")
	}
	for _, k := range allTri {
		ent := &config.ModelEntry{Model: "openai/gpt-4o", Multimodal: k == triTrue}
		if got, want := cfg.ModelMultimodal(ent), k == triTrue; got != want {
			t.Errorf("multimodal %v: ModelMultimodal = %v, want %v", k, got, want)
		}
	}
}

// capCase is one row of the matrix that pins the readers of a provider type
// other than coddy: whatever the keys say, the effective values are the ones
// the bool-typed fields gave (an absent key reads as false), and the levels,
// the default and the codex remap do not depend on them.
type capCase struct {
	providerType string
	apiModel     string
	// wantLevels is what level detection (and the codex remap) yields for the row.
	wantLevels []string
	// writeDefault is the reasoning_default written on the row, wantDefault what resolves.
	writeDefault string
	wantDefault  string
}

var capCases = []capCase{
	{"openai", "gpt-5", []string{"minimal", "low", "medium", "high"}, "high", "high"},
	{"openai", "gpt-4o", nil, "high", ""},
	{"anthropic", "claude-sonnet-5", []string{"low", "medium", "high"}, "medium", "medium"},
	{"codex", "gpt-5.5", []string{"none", "low", "medium", "high"}, "minimal", "none"},
	{"devin", "gpt-5", []string{"minimal", "low", "medium", "high"}, "low", "low"},
	{"neuraldeep", "qwen3.8-27b", []string{"low", "medium", "high"}, "", ""},
}

func TestCapabilityReadersOfOtherProviderTypesAreUnchanged(t *testing.T) {
	for _, c := range capCases {
		for _, mm := range allTri {
			for _, off := range allTri {
				cfg := &config.Config{
					Providers: []config.ProviderConfig{{Name: c.providerType, Type: c.providerType}},
					Models: []config.ModelEntry{triRow(t, config.ModelEntry{
						Model: c.providerType + "/" + c.apiModel, ReasoningDefault: c.writeDefault,
					}, mm, off)},
				}
				ent := &cfg.Models[0]
				name := ent.Model + " multimodal=" + mm.String() + " allow_reasoning_off=" + off.String()

				if got, want := cfg.ModelMultimodal(ent), mm == triTrue; got != want {
					t.Errorf("%s: ModelMultimodal = %v, want %v", name, got, want)
				}
				if got := cfg.ReasoningLevelsFor(ent); !reflect.DeepEqual(got, c.wantLevels) {
					t.Errorf("%s: ReasoningLevelsFor = %v, want %v", name, got, c.wantLevels)
				}
				wantOff := off == triTrue && len(c.wantLevels) > 0
				if got := cfg.ReasoningOffOffered(ent); got != wantOff {
					t.Errorf("%s: ReasoningOffOffered = %v, want %v", name, got, wantOff)
				}
				if got := cfg.DefaultReasoningLevelFor(ent); got != c.wantDefault {
					t.Errorf("%s: DefaultReasoningLevelFor = %q, want %q", name, got, c.wantDefault)
				}
				wantChoices := append([]string(nil), c.wantLevels...)
				if wantOff {
					wantChoices = append(wantChoices, config.ReasoningOff)
				}
				if got := cfg.ReasoningChoicesFor(ent); !reflect.DeepEqual(got, wantChoices) {
					t.Errorf("%s: ReasoningChoicesFor = %v, want %v", name, got, wantChoices)
				}
			}
		}
	}
}

// A coddy row reads its two keys as every other type does until the remote's
// listing is consulted: an absent key is false and a written value stands. The
// reasoning levels here are written, so the test does not depend on how a
// level is detected for an alias.
func TestCoddyRowReadsItsKeysAsWritten(t *testing.T) {
	levels := []string{"low", "high"}
	for _, mm := range allTri {
		for _, off := range allTri {
			cfg := &config.Config{
				Providers: []config.ProviderConfig{{Name: "far", Type: "coddy"}},
				Models: []config.ModelEntry{triRow(t, config.ModelEntry{
					Model: "far/coder", ReasoningLevels: &levels,
				}, mm, off)},
			}
			ent := &cfg.Models[0]
			if got, want := cfg.ModelMultimodal(ent), mm == triTrue; got != want {
				t.Errorf("multimodal=%v: ModelMultimodal = %v, want %v", mm, got, want)
			}
			if got, want := cfg.ReasoningOffOffered(ent), off == triTrue; got != want {
				t.Errorf("allow_reasoning_off=%v: ReasoningOffOffered = %v, want %v", off, got, want)
			}
		}
	}
}

// capYAML has one row per state of the two keys: absent, written true, written
// false and written null (which loads as absent, as it does for stream).
const capYAML = `providers:
  - name: openai
    type: openai
    api_key: k
models:
  - model: openai/absent
  - model: openai/yes
    multimodal: true
    allow_reasoning_off: true
  - model: openai/no
    multimodal: false
    allow_reasoning_off: false
  - model: openai/null
    multimodal: null
    allow_reasoning_off: null
agent:
  model: openai/absent
`

// want is the state of (multimodal, allow_reasoning_off) per row of capYAML.
var capWant = map[string][2]tri{
	"openai/absent": {triAbsent, triAbsent},
	"openai/yes":    {triTrue, triTrue},
	"openai/no":     {triFalse, triFalse},
	"openai/null":   {triAbsent, triAbsent},
}

func assertCapStates(t *testing.T, where string, cfg *config.Config) {
	t.Helper()
	for model, want := range capWant {
		e := cfg.FindModelEntry(model)
		if e == nil {
			t.Fatalf("%s: row %s is missing", where, model)
		}
		if got := mmState(e); got != mmWant(want[0]) {
			t.Errorf("%s: %s multimodal is %v, want %v", where, model, got, mmWant(want[0]))
		}
		if got := offState(e); got != want[1] {
			t.Errorf("%s: %s allow_reasoning_off is %v, want %v", where, model, got, want[1])
		}
	}
}

func TestCapabilityKeysYAMLAreTriState(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, capYAML))
	if err != nil {
		t.Fatal(err)
	}
	assertCapStates(t, "load", cfg)
}

// rowJSON returns the JSON object of one models[] row of a marshalled DTO.
func rowJSON(t *testing.T, raw []byte, model string) map[string]json.RawMessage {
	t.Helper()
	var doc struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, row := range doc.Models {
		if string(row["model"]) == `"`+model+`"` {
			return row
		}
	}
	t.Fatalf("row %s is not in %s", model, raw)
	return nil
}

func TestCapabilityKeysJSONDTORoundTrip(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, capYAML))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(config.ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	// The wire says what the file says: no key for an absent one, the value for a written one.
	for model, want := range capWant {
		row := rowJSON(t, raw, model)
		for i, key := range []string{"multimodal", "allow_reasoning_off"} {
			v, present := row[key]
			state := want[i]
			if key == "multimodal" {
				state = mmWant(state)
			}
			switch state {
			case triAbsent:
				if present {
					t.Errorf("%s: the DTO wrote an absent %s as %s", model, key, v)
				}
			case triTrue:
				if string(v) != "true" {
					t.Errorf("%s: %s = %s, want true", model, key, v)
				}
			case triFalse:
				if string(v) != "false" {
					t.Errorf("%s: %s = %s, want a written false", model, key, v)
				}
			}
		}
	}
	back, err := config.ParseAndValidateConfigJSON(raw, cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	assertCapStates(t, "json round trip", back)

	// A client that sends null for a key says the same as one that omits it.
	nullBody := strings.Replace(string(raw), `"model":"openai/absent"`, `"model":"openai/absent","multimodal":null,"allow_reasoning_off":null`, 1)
	if nullBody == string(raw) {
		t.Fatalf("test fixture: the absent row was not found in %s", raw)
	}
	viaNull, err := config.ParseAndValidateConfigJSON([]byte(nullBody), cfg.Paths)
	if err != nil {
		t.Fatalf("null keys: %v", err)
	}
	assertCapStates(t, "json null", viaNull)
}

// What a settings save writes: an absent allow_reasoning_off stays out of the
// file and a written false stays in it. (multimodal is a plain boolean, whose
// false is the absent key, so its spelling in the file is not checked.)
func TestCapabilityKeysSurviveASettingsSave(t *testing.T) {
	p := writeConfig(t, capYAML)
	cur, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(config.ConfigToJSONDTO(cur))
	if err != nil {
		t.Fatal(err)
	}
	reloaded := saveThroughSettings(t, p, body)
	assertCapStates(t, "after the settings save", reloaded)

	written, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if section := rowSection(t, string(written), "openai/absent"); strings.Contains(section, "allow_reasoning_off") {
		t.Errorf("the save wrote allow_reasoning_off for a row that never had it:\n%s", section)
	}
	if section := rowSection(t, string(written), "openai/no"); !strings.Contains(section, "allow_reasoning_off: false") {
		t.Errorf("the save dropped a written allow_reasoning_off: false:\n%s", section)
	}
}

// The writer the settings route uses (MarshalConfigYAMLForEdit over the file)
// saves allow_reasoning_off as the form sent it. A row the form adds with no
// allow_reasoning_off stays without one, so a model of a coddy provider goes on
// following the remote's listing after the next load. A false the form sends
// is written, also for a row whose file had no key (the operator turning off
// what the listing offers), and a written false stays written. A row the form
// sends back without the key (the switch at "remote") loses it in the file.
func TestSettingsSaveWritesAllowReasoningOffAsTheFormSentIt(t *testing.T) {
	p := writeConfig(t, `providers:
  - name: far
    type: coddy
    api_base: http://127.0.0.1:1
models:
  - model: far/pinned
    allow_reasoning_off: false
  - model: far/inherit
  - model: far/back
    allow_reasoning_off: false
agent:
  model: far/pinned
`)
	live, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	served, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(config.ConfigToJSONDTO(live))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	models, _ := doc["models"].([]any)
	for _, m := range models {
		switch row, _ := m.(map[string]any); row["model"] {
		case "far/inherit":
			row["allow_reasoning_off"] = false
		case "far/back":
			delete(row, "allow_reasoning_off")
		}
	}
	doc["models"] = append(models,
		map[string]any{"model": "far/coder"},
		map[string]any{"model": "far/written", "allow_reasoning_off": false})
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	next, err := config.ParseConfigJSONPreservingSecrets(body, live.Paths, live)
	if err != nil {
		t.Fatal(err)
	}
	out, err := config.MarshalConfigYAMLForEdit(next, served, live, p)
	if err != nil {
		t.Fatal(err)
	}
	if s := rowSection(t, string(out), "far/coder"); strings.Contains(s, "allow_reasoning_off") {
		t.Errorf("the save wrote allow_reasoning_off for a row the form sent without it:\n%s", s)
	}
	if err := os.WriteFile(p, out, 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	for model, want := range map[string]tri{"far/coder": triAbsent, "far/pinned": triFalse, "far/written": triFalse, "far/inherit": triFalse, "far/back": triAbsent} {
		e := reloaded.FindModelEntry(model)
		if e == nil {
			t.Fatalf("%s is missing after the save:\n%s", model, out)
		}
		if got := offState(e); got != want {
			t.Errorf("%s: allow_reasoning_off is %v after the save, want %v", model, got, want)
		}
	}
}

func TestLegacyFileWithBothKeysFalseLoadsAsExplicitFalse(t *testing.T) {
	legacy := `providers:
  - name: far
    type: coddy
    api_base: http://127.0.0.1:1
models:
  - model: far/coder
    max_tokens: 0
    temperature: 0
    max_context_tokens: 0
    multimodal: false
    reasoning_default: ""
    allow_reasoning_off: false
agent:
  model: far/coder
`
	p := writeConfig(t, legacy)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	e := cfg.FindModelEntry("far/coder")
	if e == nil || e.Multimodal || offState(e) != triFalse {
		t.Fatalf("a saved false must load as a written false: %+v", e)
	}
	if cfg.ModelMultimodal(e) || cfg.ReasoningOffOffered(e) {
		t.Fatal("a written false reads as no")
	}
	// ... and the next save keeps saying it.
	body, err := json.Marshal(config.ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	reloaded := saveThroughSettings(t, p, body)
	e = reloaded.FindModelEntry("far/coder")
	if e == nil || e.Multimodal || offState(e) != triFalse {
		t.Fatalf("the save changed a written false: %+v", e)
	}
}

// The comment-preserving writer, on an edit: rows the operator left without the
// keys do not gain them, a written false stays and its comment with it.
func TestMarshalForEditKeepsTheStateOfTheCapabilityKeys(t *testing.T) {
	doc := `# yaml-language-server: $schema=https://coddy.dev/config.schema.json
providers:
  - name: openai
    type: openai
    api_key: k
models:
  - model: openai/absent
  - model: openai/no
    multimodal: false # never send images here
    allow_reasoning_off: false
  - model: openai/yes
    multimodal: true
agent:
  model: openai/absent
`
	p := writeConfig(t, doc)
	live, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	served, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	edit, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	// The edit moves an unrelated knob.
	edit.Agent.MaxTurns = 7
	out, err := config.MarshalConfigYAMLForEdit(edit, served, live, p)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if s := rowSection(t, text, "openai/absent"); strings.Contains(s, "multimodal") || strings.Contains(s, "allow_reasoning_off") {
		t.Errorf("an absent key came back:\n%s", s)
	}
	if s := rowSection(t, text, "openai/no"); !strings.Contains(s, "multimodal: false # never send images here") || !strings.Contains(s, "allow_reasoning_off: false") {
		t.Errorf("a written false or its comment was lost:\n%s", s)
	}
	if s := rowSection(t, text, "openai/yes"); !strings.Contains(s, "multimodal: true") {
		t.Errorf("a written true was lost:\n%s", s)
	}
	if err := os.WriteFile(p, out, 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	for model, want := range map[string][2]tri{
		"openai/absent": {triAbsent, triAbsent},
		"openai/no":     {triFalse, triFalse},
		"openai/yes":    {triTrue, triAbsent},
	} {
		e := reloaded.FindModelEntry(model)
		if e == nil || mmState(e) != mmWant(want[0]) || offState(e) != want[1] {
			t.Errorf("%s reloaded as %+v, want %v", model, e, want)
		}
	}
}

// rowSection returns the lines of the models[] row whose model is the given id.
func rowSection(t *testing.T, text, model string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	var out []string
	in := false
	for _, ln := range lines {
		trim := strings.TrimSpace(ln)
		if strings.HasPrefix(trim, "- model:") {
			in = strings.Contains(trim, model)
			if in {
				out = append(out, ln)
			}
			continue
		}
		if in {
			// the row ends at the next top-level key
			if ln != "" && !strings.HasPrefix(ln, " ") {
				break
			}
			out = append(out, ln)
		}
	}
	if len(out) == 0 {
		t.Fatalf("row %s not found in:\n%s", model, text)
	}
	return strings.Join(out, "\n")
}

func TestUISchemaCapabilityKeysStayBoolean(t *testing.T) {
	doc := config.UISchemaMap()
	items := doc["properties"].(map[string]interface{})["models"].(map[string]interface{})["items"].(map[string]interface{})
	props := items["properties"].(map[string]interface{})
	for _, key := range []string{"multimodal", "allow_reasoning_off"} {
		node, ok := props[key].(map[string]interface{})
		if !ok {
			t.Fatalf("%s is missing from the UI schema", key)
		}
		if node["type"] != "boolean" {
			t.Errorf("%s: UI schema type %v, want boolean (the form draws a switch)", key, node["type"])
		}
	}
	if off := props["allow_reasoning_off"].(map[string]interface{}); off["default"] != false {
		t.Errorf("allow_reasoning_off default = %v, want false", off["default"])
	}
}
