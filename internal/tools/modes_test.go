package tools

import (
	"testing"
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
