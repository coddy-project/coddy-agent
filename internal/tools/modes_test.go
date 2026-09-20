package tools

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestRegistryIncludesWrite(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("write"); !ok {
		t.Fatal("write should be registered")
	}
	if _, ok := r.Get("config_get"); !ok {
		t.Fatal("config_get should be registered")
	}
	// Staging and reviewing are free; only the tools that touch config.yaml
	// (commit, rollback) go through the permission gate.
	for _, name := range []string{"config_set", "config_changes", "config_revert"} {
		if tool, ok := r.Get(name); !ok || tool.RequiresPermission {
			t.Fatalf("%s should be registered without a permission gate", name)
		}
	}
	for _, name := range []string{"config_commit", "config_rollback"} {
		if tool, ok := r.Get(name); !ok || !tool.RequiresPermission {
			t.Fatalf("%s should be registered and require permission", name)
		}
	}
}

func TestAllToolDefinitionsIncludesReadAndWriteText(t *testing.T) {
	r := NewRegistry()
	names := make(map[string]bool)
	for _, d := range r.AllToolDefinitions() {
		names[d.Name] = true
	}
	if !names["read"] || !names["glob"] || !names["grep"] || !names["write"] {
		t.Fatalf("expected read, glob, grep, write in full set: missing from %+v", names)
	}
}

func TestPreviewServerFollowsItsSwitchAndTheBackgroundOne(t *testing.T) {
	off := false
	if tool, ok := NewRegistry().Get("preview_server"); !ok || tool.RequiresPermission {
		t.Fatal("preview_server should be registered without a permission gate")
	}

	cfg := &config.Config{}
	cfg.Tools.PreviewServer.Enabled = &off
	if _, ok := NewRegistryFor(cfg).Get("preview_server"); ok {
		t.Fatal("tools.preview_server.enable: false should hide preview_server")
	}

	cfg = &config.Config{}
	cfg.Tools.Background.Enabled = &off
	if _, ok := NewRegistryFor(cfg).Get("preview_server"); ok {
		t.Fatal("preview_server runs in the task pool, so tools.background.enable: false should hide it")
	}
}
