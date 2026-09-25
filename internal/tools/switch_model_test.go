package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func TestSwitchModelDescriptionRequiresUserRequest(t *testing.T) {
	description := SwitchModelTool(nil).Definition.Description
	if !strings.Contains(description, "only when the user asks") {
		t.Fatalf("description does not restrict switching to a user request: %q", description)
	}
	for _, unsolicited := range []string{"hard bug", "routine work", "stronger model", "faster one"} {
		if strings.Contains(description, unsolicited) {
			t.Errorf("description still recommends unsolicited switching: %q", unsolicited)
		}
	}
}

// The model and the reasoning level the session runs on, and the summarizer of
// its compaction, are the user's to pick: those arguments say so. A subagent
// is different, and spawn_agent keeps letting the model choose the child's.
func TestSessionModelArgumentsLeaveTheChoiceToTheUser(t *testing.T) {
	cfg := &config.Config{Models: []config.ModelEntry{{Model: "fake/a"}, {Model: "fake/b"}}}
	defs := map[string]map[string]interface{}{}
	for _, def := range NewRegistryFor(cfg).AllToolDefinitions() {
		schema, _ := def.InputSchema.(map[string]interface{})
		props, _ := schema["properties"].(map[string]interface{})
		defs[def.Name] = props
	}
	for _, arg := range []struct{ tool, name string }{
		{ToolSwitchModel, "model"}, {ToolSwitchModel, "reasoning"}, {ToolCompactContext, "model"},
	} {
		prop, _ := defs[arg.tool][arg.name].(map[string]interface{})
		desc, _ := prop["description"].(string)
		if !strings.Contains(desc, tooling.ModelChoiceRule) {
			t.Errorf("%s.%s does not leave the choice to the user: %q", arg.tool, arg.name, desc)
		}
	}
	for _, arg := range []string{"model", "reasoning"} {
		prop, _ := defs[ToolSpawnAgent][arg].(map[string]interface{})
		if desc, _ := prop["description"].(string); strings.Contains(desc, tooling.ModelChoiceRule) {
			t.Errorf("spawn_agent.%s forbids the model to pick the child's %s: %q", arg, arg, desc)
		}
	}
}

func TestSwitchModelDefaultsToSessionScope(t *testing.T) {
	var got tooling.ModelSwitch
	tool := SwitchModelTool(nil)
	_, err := tool.Execute(context.Background(), `{"model":"example/model"}`, &tooling.Env{
		SwitchModel: func(_ context.Context, req tooling.ModelSwitch) (string, error) {
			got = req
			return "ok", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Session {
		t.Fatal("an unscoped user-requested model switch must last for the session")
	}
}
