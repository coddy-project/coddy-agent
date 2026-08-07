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
	if tool, ok := r.Get("config_set"); !ok || !tool.RequiresPermission {
		t.Fatal("config_set should be registered and require permission")
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
