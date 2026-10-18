package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestCanvasSourcesDefaultToTheCDNs(t *testing.T) {
	var c ToolCanvas
	if !c.ResolvedEnabled() {
		t.Fatal("the canvas is off by default")
	}
	if got := c.Sources(); !reflect.DeepEqual(got, CanvasCDNSources) {
		t.Fatalf("default sources %v", got)
	}
	off := false
	c = ToolCanvas{AllowCDN: &off, AllowedSources: []string{" https://cdn.example.com ", "https://cdn.example.com", "https://*.assets.example"}}
	if got := c.Sources(); !reflect.DeepEqual(got, []string{"https://cdn.example.com", "https://*.assets.example"}) {
		t.Fatalf("sources without the CDNs %v", got)
	}
}

func TestCanvasRefusesASourceThatOpensEverything(t *testing.T) {
	for _, bad := range []string{"https:", "*", "'unsafe-inline'", "https://a.example; script-src *", "data:"} {
		tools := Tools{Canvas: ToolCanvas{AllowedSources: []string{bad}}}
		err := tools.Validate()
		if err == nil || !strings.Contains(err.Error(), "tools.canvas.allowed_sources[0]") {
			t.Errorf("source %q: %v", bad, err)
		}
	}
}

func TestCanvasSectionSurvivesTheJSONRoundTrip(t *testing.T) {
	on, off := true, false
	cfg := &Config{Tools: Tools{Canvas: ToolCanvas{Enabled: &on, AllowCDN: &off, AllowedSources: []string{"https://cdn.example.com"}}}}
	back := JSONDTOToConfig(ConfigToJSONDTO(cfg), Paths{})
	got := back.Tools.Canvas
	if got.Enabled == nil || !*got.Enabled || got.AllowCDN == nil || *got.AllowCDN || !reflect.DeepEqual(got.AllowedSources, []string{"https://cdn.example.com"}) {
		t.Fatalf("round trip %+v", got)
	}
}
