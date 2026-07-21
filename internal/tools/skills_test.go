package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestManageSkillsToolDefinition(t *testing.T) {
	tool := ManageSkillsTool(&config.Config{})
	if tool.Definition.Name != "manage_skills" {
		t.Fatalf("name = %q", tool.Definition.Name)
	}
	schema, _ := tool.Definition.InputSchema.(map[string]interface{})
	props, _ := schema["properties"].(map[string]interface{})
	action, _ := props["action"].(map[string]interface{})
	enum, _ := action["enum"].([]interface{})
	want := map[string]bool{
		"list": true, "list_sources": true, "add_source": true, "remove_source": true,
		"sync": true, "check_updates": true, "update": true,
	}
	if len(enum) != len(want) {
		t.Fatalf("action enum size = %d, want %d (%v)", len(enum), len(want), enum)
	}
	for _, v := range enum {
		if !want[v.(string)] {
			t.Errorf("unexpected action %q", v)
		}
	}
	if !tool.RequiresPermission {
		t.Error("manage_skills should require permission (it mutates config and clones repos)")
	}
}

func TestManageSkillsListSourcesEmpty(t *testing.T) {
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}}
	out, err := executeManageSkills(context.Background(), cfg, `{"action":"list_sources"}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No marketplace sources") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestManageSkillsErrorPaths(t *testing.T) {
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}}
	cases := []struct{ name, args string }{
		{"unknown action", `{"action":"frobnicate"}`},
		{"add without source", `{"action":"add_source"}`},
		{"remove without source", `{"action":"remove_source"}`},
	}
	for _, tc := range cases {
		if _, err := executeManageSkills(context.Background(), cfg, tc.args, nil); err == nil {
			t.Errorf("%s: expected error", tc.name)
		}
	}
}

func TestManageSkillsNilConfig(t *testing.T) {
	if _, err := executeManageSkills(context.Background(), nil, `{"action":"list"}`, nil); err == nil {
		t.Error("expected error with nil config")
	}
}
