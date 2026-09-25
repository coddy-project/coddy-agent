package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func TestSpawnAgentWakesByDefaultUnlessExplicitlyDisabled(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
		wake bool
	}{
		{"omitted", `{"agent":"general","prompt":"inspect","background":true}`, true},
		{"explicit false", `{"agent":"general","prompt":"inspect","background":true,"notify_on_finish":false}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got tooling.SpawnRequest
			_, err := SpawnAgentTool().Execute(context.Background(), tc.args, &tooling.Env{
				SpawnAgent: func(_ context.Context, req tooling.SpawnRequest) (string, error) {
					got = req
					return "started", nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.NotifyOnFinish != tc.wake {
				t.Fatalf("notify_on_finish = %v, want %v", got.NotifyOnFinish, tc.wake)
			}
		})
	}
}

// A detached child wakes the parent by default, and the tool says so: text that
// only tells the model to collect the report later keeps it waiting for a run
// it could leave to wake it.
func TestSpawnAgentDescribesTheDefaultWake(t *testing.T) {
	def := SpawnAgentTool().Definition
	schema, _ := def.InputSchema.(map[string]interface{})
	props, _ := schema["properties"].(map[string]interface{})
	background, _ := props["background"].(map[string]interface{})
	desc, _ := background["description"].(string)
	for name, text := range map[string]string{"description": def.Description, "background": desc} {
		if !strings.Contains(text, "wakes you") {
			t.Errorf("spawn_agent %s does not say a detached run wakes the parent: %q", name, text)
		}
	}
}
