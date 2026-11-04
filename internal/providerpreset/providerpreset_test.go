package providerpreset

import (
	"os"
	"path/filepath"
	"regexp"
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
	p, ok := ByID("ollama")
	if !ok || p.ProviderName != "local" {
		t.Fatalf("ByID(ollama) = %+v, %v", p, ok)
	}
}

func TestModelRefNamesTheProviderRow(t *testing.T) {
	ollama, _ := ByID("ollama")
	compatible, _ := ByID("compatible")
	for _, tc := range []struct {
		preset Preset
		in     string
		want   string
	}{
		{ollama, "llama3.2", "local/llama3.2"},
		{ollama, "local/llama3.2", "local/llama3.2"},
		{ollama, "   ", "local/llama3.2"},
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
		"ollama":     "http://127.0.0.1:11434/v1",
		"compatible": "",
		"openai":     "",
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
	webEntryRe  = regexp.MustCompile(`(?s)\{\s*\n\s*id: "([a-z]+)",(.*?)\n  \}`)
	webFieldRe  = regexp.MustCompile(`\b(providerName|providerType|defaultModel): "([^"]*)"`)
	webBoolRe   = regexp.MustCompile(`\bmultimodal: (true|false)`)
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
			}
		}
		if m := webBoolRe.FindSubmatch(entry[2]); m != nil {
			p.Multimodal = string(m[1]) == "true"
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
