package providerpreset

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// The console's picker and the web UI's show the same providers in the same
// order, so a person who set Coddy up in one recognises the other. The two
// catalogs are written in two languages, and this is what keeps them in step:
// the TypeScript twin is read as text and its presets compared with this
// package's, field by field.
func TestPresetsMatchTheWebPicker(t *testing.T) {
	web := readWebPresets(t)
	if len(web) != len(Presets) {
		t.Fatalf("the web picker offers %d presets, this package %d: %v vs %v",
			len(web), len(Presets), ids(web), ids(Presets))
	}
	for i, want := range Presets {
		got := web[i]
		switch {
		case got.ID != want.ID:
			t.Fatalf("preset %d is %q in the web picker and %q here; the order is the picker's order", i, got.ID, want.ID)
		case got.ProviderName != want.ProviderName:
			t.Errorf("%s writes the provider row %q in the web picker and %q here", want.ID, got.ProviderName, want.ProviderName)
		case got.ProviderType != want.ProviderType:
			t.Errorf("%s is type %q in the web picker and %q here", want.ID, got.ProviderType, want.ProviderType)
		case got.DefaultModel != want.DefaultModel:
			t.Errorf("%s starts on %q in the web picker and %q here", want.ID, got.DefaultModel, want.DefaultModel)
		case got.Multimodal != want.Multimodal:
			t.Errorf("%s reads images %v in the web picker and %v here", want.ID, got.Multimodal, want.Multimodal)
		case got.APIBaseDefault != want.DefaultAPIBase():
			t.Errorf("%s starts on the endpoint %q in the web picker and %q here", want.ID, got.APIBaseDefault, want.DefaultAPIBase())
		case got.MaxContextTokens != want.MaxContextTokens:
			t.Errorf("%s has the window %d in the web picker and %d here", want.ID, got.MaxContextTokens, want.MaxContextTokens)
		}
	}
}

// The endpoints a preset offers are a line a person reads, and the two
// pickers must offer the same line: the console takes it from APIBases here,
// the web UI from a dictionary key, and nothing but this test connects them.
// They drifted once already - an em dash in the dictionary against a hyphen
// here - because the structural parity above compares values, not labels.
func TestTheEndpointLabelsMatchTheWebPicker(t *testing.T) {
	web := readWebAPIBases(t)
	english := readWebMessages(t)
	for _, preset := range Presets {
		if len(preset.APIBases) == 0 {
			continue
		}
		list, ok := web[preset.ID]
		if !ok {
			t.Errorf("%s offers %d endpoints here and none in the web picker", preset.ID, len(preset.APIBases))
			continue
		}
		if len(list) != len(preset.APIBases) {
			t.Errorf("%s offers %d endpoints in the web picker and %d here", preset.ID, len(list), len(preset.APIBases))
			continue
		}
		for i, want := range preset.APIBases {
			got := list[i]
			if got.Value != want.Value {
				t.Errorf("%s endpoint %d is %q in the web picker and %q here", preset.ID, i, got.Value, want.Value)
			}
			label, ok := english[got.Label]
			if !ok {
				t.Errorf("%s endpoint %d names the message %q, which the English dictionary does not carry", preset.ID, i, got.Label)
				continue
			}
			if label != want.Label {
				t.Errorf("%s endpoint %d reads %q in the web picker and %q here", preset.ID, i, label, want.Label)
			}
		}
	}
}

// NeuralDeep is first, because that is what both pickers open on.
func TestDefaultIsNeuralDeep(t *testing.T) {
	if got := Default().ID; got != "neuraldeep" {
		t.Fatalf("a picker opens on %q, want neuraldeep", got)
	}
	if _, ok := ByID("nothing"); ok {
		t.Fatal("ByID invented a preset")
	}
	p, ok := ByID("compatible")
	if !ok || p.ProviderName != "custom" {
		t.Fatalf("ByID(compatible) = %+v, %v", p, ok)
	}
}

func TestModelRefNamesTheProviderRow(t *testing.T) {
	acp, _ := ByID("acp")
	compatible, _ := ByID("compatible")
	for _, tc := range []struct {
		preset Preset
		in     string
		want   string
	}{
		{compatible, "llama3.2", "custom/llama3.2"},
		{compatible, "custom/llama3.2", "custom/llama3.2"},
		{compatible, "   ", "custom/gpt-4o"},
		{acp, "sonnet", "acp/sonnet"},
		// A slash inside the id a provider lists is part of that id: the
		// first segment of a reference is always the provider row.
		{compatible, "openai/gpt-4o", "custom/openai/gpt-4o"},
	} {
		if got := tc.preset.ModelRef(tc.in); got != tc.want {
			t.Errorf("%s.ModelRef(%q) = %q, want %q", tc.preset.ID, tc.in, got, tc.want)
		}
	}
}

func TestDefaultAPIBase(t *testing.T) {
	for id, want := range map[string]string{
		"neuraldeep": "https://api.neuraldeep.ru/v1",
		"opencode":   "https://opencode.ai/inference/go/v1",
		"compatible": "",
		"openai":     "",
		"acp":        "",
	} {
		p, ok := ByID(id)
		if !ok {
			t.Fatalf("no preset %q", id)
		}
		if got := p.DefaultAPIBase(); got != want {
			t.Errorf("%s starts on %q, want %q", id, got, want)
		}
	}
}

// webPresetsFile is the TypeScript catalog, relative to this package.
const webPresetsFile = "../../external/ui/src/ui/onboarding/providerConfig.ts"

var (
	webEntryRe = regexp.MustCompile(`(?s)\{\s*\n\s*id: "([a-z]+)",(.*?)\n  \}`)
	webFieldRe = regexp.MustCompile(`\b(providerName|providerType|defaultModel|apiBase): "([^"]*)"`)
	webBoolRe  = regexp.MustCompile(`\bmultimodal: (true|false)`)
	webNumRe   = regexp.MustCompile(`\bmaxContextTokens: (\d+)`)
	// An endpoint written as the first entry of a named option list rather
	// than as a literal: NeuralDeep's hubs and OpenCode's gateways.
	webOptionRe  = regexp.MustCompile(`\bapiBase: ([A-Z_]+)\[0]\.value`)
	webOptionsRe = regexp.MustCompile(
		`(?s)export const ([A-Z_]+) = \[\s*\{\s*\n\s*value: "([^"]*)"`)
	webArrayRe  = regexp.MustCompile(`(?s)export const PROVIDER_PRESETS[^=]*= \[(.*?)\n\];`)
	errNoWebArr = "external/ui/src/ui/onboarding/providerConfig.ts has no PROVIDER_PRESETS array; keep the two catalogs in step by hand"
)

func readWebPresets(t *testing.T) []Preset {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(webPresetsFile))
	if err != nil {
		t.Fatalf("read the web catalog: %v", err)
	}
	body := webArrayRe.FindSubmatch(raw)
	if body == nil {
		t.Fatal(errNoWebArr)
	}
	// The first value of every exported option list, for an apiBase written
	// as OPTIONS[0].value instead of a literal.
	options := map[string]string{}
	for _, m := range webOptionsRe.FindAllSubmatch(raw, -1) {
		options[string(m[1])] = string(m[2])
	}
	var out []Preset
	for _, entry := range webEntryRe.FindAllSubmatch(body[1], -1) {
		p := Preset{ID: string(entry[1])}
		for _, field := range webFieldRe.FindAllSubmatch(entry[2], -1) {
			switch string(field[1]) {
			case "providerName":
				p.ProviderName = string(field[2])
			case "providerType":
				p.ProviderType = string(field[2])
			case "defaultModel":
				p.DefaultModel = string(field[2])
			case "apiBase":
				p.APIBaseDefault = string(field[2])
			}
		}
		if m := webOptionRe.FindSubmatch(entry[2]); m != nil {
			base, ok := options[string(m[1])]
			if !ok {
				t.Fatalf("%s: the web catalog names the option list %s, which it does not export", p.ID, m[1])
			}
			p.APIBaseDefault = base
		}
		if m := webBoolRe.FindSubmatch(entry[2]); m != nil {
			p.Multimodal = string(m[1]) == "true"
		}
		if m := webNumRe.FindSubmatch(entry[2]); m != nil {
			n, err := strconv.Atoi(string(m[1]))
			if err != nil {
				t.Fatalf("%s: maxContextTokens %q: %v", p.ID, m[1], err)
			}
			p.MaxContextTokens = n
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		t.Fatal(errNoWebArr)
	}
	return out
}

func ids(list []Preset) []string {
	out := make([]string, 0, len(list))
	for _, p := range list {
		out = append(out, p.ID)
	}
	return out
}

// webMessagesFile is the English dictionary of the SPA, where the web
// picker's endpoint labels live.
const webMessagesFile = "../../external/ui/src/ui/i18n/messages/en.ts"

var (
	// One entry of an exported option list: its value and the dictionary key
	// its label is written as.
	webOptionEntryRe = regexp.MustCompile(
		`value: "([^"]*)",\s*\n\s*labelKey: "([^"]*)"`)
	webOptionListRe = regexp.MustCompile(`(?s)export const ([A-Z_]+) = \[(.*?)\n\] as const;`)
	// A dictionary entry, whose value Prettier may have moved to the next
	// line. Only the single-line string form is read; a label long enough to
	// be split over several string literals would need more than this, and
	// an endpoint label is never that long.
	webMessageRe = regexp.MustCompile(`\n  "([^"]+)":\s*\n?\s*"((?:[^"\\]|\\.)*)",`)
)

// readWebAPIBases reads the endpoint lists the web catalog exports, keyed by
// the preset that names one, each entry carrying its value and the dictionary
// key of its label.
func readWebAPIBases(t *testing.T) map[string][]APIBase {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(webPresetsFile))
	if err != nil {
		t.Fatalf("read the web catalog: %v", err)
	}
	lists := map[string][]APIBase{}
	for _, m := range webOptionListRe.FindAllSubmatch(raw, -1) {
		var entries []APIBase
		for _, e := range webOptionEntryRe.FindAllSubmatch(m[2], -1) {
			entries = append(entries, APIBase{Value: string(e[1]), Label: string(e[2])})
		}
		lists[string(m[1])] = entries
	}
	body := webArrayRe.FindSubmatch(raw)
	if body == nil {
		t.Fatal(errNoWebArr)
	}
	out := map[string][]APIBase{}
	for _, entry := range webEntryRe.FindAllSubmatch(body[1], -1) {
		m := webOptionRe.FindSubmatch(entry[2])
		if m == nil {
			continue
		}
		list, ok := lists[string(m[1])]
		if !ok {
			t.Fatalf("%s: the web catalog names the option list %s, which it does not export", entry[1], m[1])
		}
		out[string(entry[1])] = list
	}
	return out
}

// readWebMessages reads the English dictionary into a map. A key whose value
// the regexp cannot read is left out, and the caller reports it as missing.
func readWebMessages(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(webMessagesFile))
	if err != nil {
		t.Fatalf("read the English dictionary: %v", err)
	}
	out := map[string]string{}
	for _, m := range webMessageRe.FindAllSubmatch(raw, -1) {
		out[string(m[1])] = string(m[2])
	}
	if len(out) == 0 {
		t.Fatal("the English dictionary read as empty; keep the two catalogs in step by hand")
	}
	return out
}

// Both pickers offer the same mode and region combinations in the same order.
func TestKimiServicesMatchTheWebPicker(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash("../../external/ui/src/ui/onboarding/ProviderPickerDialog.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	matches := regexp.MustCompile(`<option value="((?:subscription|platform):(?:global|china))">`).FindAllSubmatch(raw, -1)
	if len(matches) != len(KimiServices) {
		t.Fatalf("web offers %d Kimi services, console %d", len(matches), len(KimiServices))
	}
	for i, service := range KimiServices {
		if got, want := string(matches[i][1]), service.Mode+":"+service.Region; got != want {
			t.Errorf("service %d: web %q console %q", i, got, want)
		}
	}
}
