package agent

import (
	"path/filepath"
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

// TestPreviewServerSettingsMirrorTooling is the same guard for
// previewServerSettings.
func TestPreviewServerSettingsMirrorTooling(t *testing.T) {
	from := reflect.TypeOf(config.ToolPreviewServerSettings{})
	to := reflect.TypeOf(tooling.PreviewServerSettings{})
	if from.NumField() != to.NumField() {
		t.Fatalf("field count: config has %d, tooling has %d", from.NumField(), to.NumField())
	}
	for i := 0; i < from.NumField(); i++ {
		a, b := from.Field(i), to.Field(i)
		if a.Name != b.Name || a.Type != b.Type {
			t.Errorf("field %d: config %s %s, tooling %s %s", i, a.Name, a.Type, b.Name, b.Type)
		}
	}
}

func TestPreviewServerSettingsCarryTheConfiguredHosts(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.PreviewServer = config.ToolPreviewServer{Host: " 0.0.0.0 ", PublicHost: "dev.example"}
	got := previewServerSettings(cfg)
	if got == nil || !got.Enabled || got.Host != "0.0.0.0" || got.PublicHost != "dev.example" {
		t.Fatalf("settings: %+v", got)
	}
	if def := previewServerSettings(&config.Config{}); def.Host != config.PreviewServerDefaultHost {
		t.Fatalf("default host: %+v", def)
	}
}

// TestPreviewPublishSettingsMirrorTooling is the same guard for
// previewPublishSettings.
func TestPreviewPublishSettingsMirrorTooling(t *testing.T) {
	from := reflect.TypeOf(config.ToolPreviewPublishSettings{})
	to := reflect.TypeOf(tooling.PreviewPublishSettings{})
	if from.NumField() != to.NumField() {
		t.Fatalf("field count: config has %d, tooling has %d", from.NumField(), to.NumField())
	}
	for i := 0; i < from.NumField(); i++ {
		a, b := from.Field(i), to.Field(i)
		if a.Name != b.Name || a.Type != b.Type {
			t.Errorf("field %d: config %s %s, tooling %s %s", i, a.Name, a.Type, b.Name, b.Type)
		}
	}
}

func TestPreviewPublishSettingsAreNilUntilTheChannelIsOn(t *testing.T) {
	if got := previewPublishSettings(nil); got != nil {
		t.Fatalf("no configuration should mean no publishing, got %+v", got)
	}
	if got := previewPublishSettings(&config.Config{}); got != nil {
		t.Fatalf("publishing is off by default, got %+v", got)
	}
}

func TestPreviewPublishSettingsResolveTheRootAndTheAddress(t *testing.T) {
	cfg := &config.Config{}
	cfg.Paths.Home = filepath.Join("/tmp", "coddy-home")
	cfg.Tools.PreviewPublish = config.ToolPreviewPublish{Enable: true, Host: "127.0.0.1", Port: 9090}
	got := previewPublishSettings(cfg)
	if got == nil || !got.Enabled {
		t.Fatalf("settings: %+v", got)
	}
	if want := filepath.Join(cfg.Paths.Home, "previews"); got.Root != want {
		t.Fatalf("root = %q, want %q", got.Root, want)
	}
	// Without a public URL the origin's own address stands in, with the
	// trailing slash a relative asset of a published page needs.
	if got.PublicURL != "http://127.0.0.1:9090/" {
		t.Fatalf("public URL = %q", got.PublicURL)
	}
	if got.TTLSeconds != config.PreviewPublishDefaultTTLSeconds || got.MaxBytes != config.PreviewPublishDefaultMaxBytes {
		t.Fatalf("defaults: %+v", got)
	}

	cfg.Tools.PreviewPublish.PublicURL = "https://coddy.example/preview"
	if got := previewPublishSettings(cfg); got.PublicURL != "https://coddy.example/preview/" {
		t.Fatalf("a public URL without a trailing slash should get one, got %q", got.PublicURL)
	}
}
