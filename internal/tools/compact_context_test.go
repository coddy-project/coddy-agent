package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func TestCompactContextToolListsTheConfiguredModels(t *testing.T) {
	tool := CompactContextTool(&config.Config{Models: []config.ModelEntry{{Model: "hub/qwen3"}, {Model: "openai/gpt-4o"}}})
	if !strings.Contains(tool.Definition.Description, "- hub/qwen3") || !strings.Contains(tool.Definition.Description, "- openai/gpt-4o") {
		t.Fatalf("description does not list the models: %q", tool.Definition.Description)
	}
	props := tool.Definition.InputSchema.(map[string]interface{})["properties"].(map[string]interface{})
	if _, ok := props["model"]; !ok {
		t.Fatalf("schema has no model property: %v", props)
	}
	if bare := CompactContextTool(nil); strings.Contains(bare.Definition.Description, "Configured models") {
		t.Fatalf("no configuration, yet models are listed: %q", bare.Definition.Description)
	}
}

func TestCompactContextPassesTheModelAndInstructions(t *testing.T) {
	var got tooling.CompactRequest
	env := &tooling.Env{CompactSession: func(_ context.Context, req tooling.CompactRequest) (string, error) {
		got = req
		return "Context compacted.", nil
	}}
	out, err := executeCompactContext(context.Background(), `{"model":" qwen ","instructions":" keep the paths "}`, env)
	if err != nil || out != "Context compacted." {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if got.Model != "qwen" || got.Instructions != "keep the paths" {
		t.Fatalf("request = %+v", got)
	}
	if !env.ContextCompacted {
		t.Fatal("the loop was not told to rebuild its messages")
	}
}

func TestCompactContextWithoutARuntime(t *testing.T) {
	if _, err := executeCompactContext(context.Background(), `{}`, &tooling.Env{}); err == nil {
		t.Fatal("want an error when the runtime wires no compaction")
	}
}
