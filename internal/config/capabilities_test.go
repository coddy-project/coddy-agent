package config_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

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

// ptr is the pointer a row holds for this state of the key: nil when absent.
func (k tri) ptr() *bool {
	switch k {
	case triTrue:
		return config.BoolPtr(true)
	case triFalse:
		return config.BoolPtr(false)
	default:
		return nil
	}
}

var allTri = []tri{triAbsent, triTrue, triFalse}

func TestBoolPtr(t *testing.T) {
	yes, no := config.BoolPtr(true), config.BoolPtr(false)
	if yes == nil || !*yes || no == nil || *no {
		t.Fatalf("BoolPtr(true) = %v, BoolPtr(false) = %v", yes, no)
	}
	if a, b := config.BoolPtr(true), config.BoolPtr(true); a == b {
		t.Fatal("BoolPtr must hand out a fresh pointer on every call, so two rows never share one")
	}
}

func TestModelMultimodalIsNilSafeAndReadsTheKey(t *testing.T) {
	var nilCfg *config.Config
	if nilCfg.ModelMultimodal(&config.ModelEntry{Model: "a/b", Multimodal: config.BoolPtr(true)}) != true {
		t.Error("a nil config must still answer from the key written on the row")
	}
	cfg := &config.Config{}
	if cfg.ModelMultimodal(nil) {
		t.Error("a missing row reads no images")
	}
	for _, k := range allTri {
		ent := &config.ModelEntry{Model: "openai/gpt-4o", Multimodal: k.ptr()}
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
					Models: []config.ModelEntry{{
						Model: c.providerType + "/" + c.apiModel, ReasoningDefault: c.writeDefault,
						Multimodal: mm.ptr(), AllowReasoningOff: off.ptr(),
					}},
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
				Models: []config.ModelEntry{{
					Model: "far/coder", ReasoningLevels: &levels,
					Multimodal: mm.ptr(), AllowReasoningOff: off.ptr(),
				}},
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

func stateOf(p *bool) tri {
	switch {
	case p == nil:
		return triAbsent
	case *p:
		return triTrue
	default:
		return triFalse
	}
}

func assertCapStates(t *testing.T, where string, cfg *config.Config) {
	t.Helper()
	for model, want := range capWant {
		e := cfg.FindModelEntry(model)
		if e == nil {
			t.Fatalf("%s: row %s is missing", where, model)
		}
		if got := stateOf(e.Multimodal); got != want[0] {
			t.Errorf("%s: %s multimodal is %v, want %v", where, model, got, want[0])
		}
		if got := stateOf(e.AllowReasoningOff); got != want[1] {
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
			switch want[i] {
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

func TestCapabilityKeyPointersAreNotSharedWithTheDTO(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "openai", Type: "openai"}},
		Models: []config.ModelEntry{{
			Model: "openai/gpt-4o", Multimodal: config.BoolPtr(true), AllowReasoningOff: config.BoolPtr(false),
		}},
	}
	dto := config.ConfigToJSONDTO(cfg)
	if dto.Models[0].Multimodal == cfg.Models[0].Multimodal || dto.Models[0].AllowReasoningOff == cfg.Models[0].AllowReasoningOff {
		t.Fatal("ConfigToJSONDTO shares a key pointer with the live config")
	}
	*dto.Models[0].Multimodal = false
	*dto.Models[0].AllowReasoningOff = true
	if !*cfg.Models[0].Multimodal || *cfg.Models[0].AllowReasoningOff {
		t.Fatal("writing through the DTO changed the live config")
	}

	next := config.JSONDTOToConfig(dto, cfg.Paths)
	if next.Models[0].Multimodal == dto.Models[0].Multimodal || next.Models[0].AllowReasoningOff == dto.Models[0].AllowReasoningOff {
		t.Fatal("JSONDTOToConfig shares a key pointer with the DTO")
	}
	*next.Models[0].Multimodal = true
	if *dto.Models[0].Multimodal {
		t.Fatal("writing through the config changed the DTO")
	}
}

// What a settings save writes: an absent key stays out of the file, a written
// false stays in it, and a file saved before the keys had three states (every
// row carried `multimodal: false` and `allow_reasoning_off: false`) loads as
// the explicit false it says.
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
	section := rowSection(t, string(written), "openai/absent")
	for _, key := range []string{"multimodal", "allow_reasoning_off"} {
		if strings.Contains(section, key) {
			t.Errorf("the save wrote %s for a row that never had it:\n%s", key, section)
		}
	}
	section = rowSection(t, string(written), "openai/no")
	for _, key := range []string{"multimodal: false", "allow_reasoning_off: false"} {
		if !strings.Contains(section, key) {
			t.Errorf("the save dropped a written %q:\n%s", key, section)
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
	if e == nil || e.Multimodal == nil || *e.Multimodal || e.AllowReasoningOff == nil || *e.AllowReasoningOff {
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
	if e == nil || e.Multimodal == nil || *e.Multimodal || e.AllowReasoningOff == nil || *e.AllowReasoningOff {
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
		if e == nil || stateOf(e.Multimodal) != want[0] || stateOf(e.AllowReasoningOff) != want[1] {
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

func TestUISchemaCapabilityKeysStayBooleanAndSayWhatAbsenceMeans(t *testing.T) {
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
		desc, _ := node["description"].(string)
		if !strings.Contains(desc, "coddy") || !strings.Contains(desc, "listing") {
			t.Errorf("%s: the description must say that for a coddy provider an absent key follows the remote's listing: %q", key, desc)
		}
	}
	if off := props["allow_reasoning_off"].(map[string]interface{}); off["default"] != false {
		t.Errorf("allow_reasoning_off default = %v, want false", off["default"])
	}
}

// The published schema accepts null for both keys (the precedent is stream),
// says what absence means, and keeps its non-coddy default.
func TestSchemaFileCapabilityKeysAreNullable(t *testing.T) {
	var doc map[string]interface{}
	if err := json.Unmarshal(config.ConfigSchemaJSON(), &doc); err != nil {
		t.Fatal(err)
	}
	items := doc["properties"].(map[string]interface{})["models"].(map[string]interface{})["items"].(map[string]interface{})
	props := items["properties"].(map[string]interface{})
	for _, key := range []string{"multimodal", "allow_reasoning_off"} {
		node := props[key].(map[string]interface{})
		if got, want := node["type"], []interface{}{"boolean", "null"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: schema type %v, want %v", key, got, want)
		}
		if node["default"] != false {
			t.Errorf("%s: schema default %v, want false", key, node["default"])
		}
		desc, _ := node["description"].(string)
		if !strings.Contains(desc, "coddy") || !strings.Contains(desc, "listing") {
			t.Errorf("%s: the description must say what absence means for a coddy provider: %q", key, desc)
		}
	}
}
