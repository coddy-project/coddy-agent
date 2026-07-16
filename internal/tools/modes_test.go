package tools

import (
	"testing"
)

func TestRegistryIncludesWrite(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("write"); !ok {
		t.Fatal("write should be registered")
	}
}

func TestAllToolDefinitionsIncludesReadAndWriteText(t *testing.T) {
	r := NewRegistry()
	names := make(map[string]bool)
	for _, d := range r.AllToolDefinitions() {
		names[d.Name] = true
	}
	if !names["read"] || !names["glob"] || !names["grep"] || !names["rg_tool"] || !names["write"] {
		t.Fatalf("expected read, glob, grep, rg_tool, write in full set: missing from %+v", names)
	}
}
