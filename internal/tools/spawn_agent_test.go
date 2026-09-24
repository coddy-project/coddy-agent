package tools

import (
	"context"
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
