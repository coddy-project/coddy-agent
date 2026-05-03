package tools

import (
	"testing"
)

func TestWriteTextOnlyInPlanModeToolList(t *testing.T) {
	r := NewRegistry()
	var agentHas, planHas bool
	for _, d := range r.ToolsForMode("agent") {
		if d.Name == "write_text_file" {
			agentHas = true
		}
	}
	for _, d := range r.ToolsForMode("plan") {
		if d.Name == "write_text_file" {
			planHas = true
		}
	}
	if agentHas {
		t.Error("write_text_file should not appear in agent mode tool list")
	}
	if !planHas {
		t.Error("write_text_file should appear in plan mode tool list")
	}
}

func TestPlanModeProvidesFSReadTools(t *testing.T) {
	r := NewRegistry()
	names := make(map[string]bool)
	for _, d := range r.ToolsForMode("plan") {
		names[d.Name] = true
	}
	if !names["read_file"] || !names["list_dir"] {
		t.Fatalf("missing read-ish tools in plan mode: %+v", names)
	}
}

func TestNewFSToolsRegistered(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"mkdir", "rmdir", "touch", "rm", "mv"} {
		if _, ok := r.Get(name); !ok {
			t.Errorf("%s should be registered", name)
		}
	}
}
