package agent

import (
	"reflect"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// TestWebSearchSettingsMirrorTooling guards the direct conversion in
// webSearchSettings. internal/config does not import internal/tooling, so the
// two structs are held in step by this test rather than by the compiler: a
// field added to one and not the other, or reordered, changes what the search
// tool reads without any build breaking.
func TestWebSearchSettingsMirrorTooling(t *testing.T) {
	from := reflect.TypeOf(config.ToolWebSearchSettings{})
	to := reflect.TypeOf(tooling.WebSearchSettings{})
	if from.NumField() != to.NumField() {
		t.Fatalf("field count: config has %d, tooling has %d", from.NumField(), to.NumField())
	}
	for i := 0; i < from.NumField(); i++ {
		a, b := from.Field(i), to.Field(i)
		if a.Name != b.Name {
			t.Errorf("field %d: config %q, tooling %q", i, a.Name, b.Name)
		}
		if a.Type != b.Type {
			t.Errorf("field %q: config %s, tooling %s", a.Name, a.Type, b.Type)
		}
	}
	if !from.ConvertibleTo(to) {
		t.Fatal("config.ToolWebSearchSettings is no longer convertible to tooling.WebSearchSettings")
	}
}

func TestWebSearchSettingsCarryTheConfiguredEngines(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.WebSearch = config.ToolWebSearch{
		Engines:     []string{"searxng", "brave"},
		SearXNGURL:  "http://localhost:8080",
		BraveAPIKey: "key",
	}
	got := webSearchSettings(cfg)
	if got == nil {
		t.Fatal("nil settings")
	}
	if !reflect.DeepEqual(got.Engines, []string{"searxng", "brave"}) {
		t.Errorf("engines: %v", got.Engines)
	}
	if got.SearXNGURL != "http://localhost:8080" || got.BraveAPIKey != "key" {
		t.Errorf("settings: %+v", got)
	}
	if got.EngineTimeoutSeconds != config.WebSearchDefaultEngineTimeoutSeconds {
		t.Errorf("unset knobs should take their defaults: %+v", got)
	}
}

func TestWebSearchSettingsNilConfigIsNilSettings(t *testing.T) {
	if got := webSearchSettings(nil); got != nil {
		t.Fatalf("got %+v", got)
	}
}
