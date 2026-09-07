//go:build http && ui

package ui

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Run the DOM assertion in Vitest, where the actual React component is rendered.
// The http,ui test matrix installs these dependencies through make ui-build.
func TestSpawnAgentCardFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "spawn_agent_card",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^the spawn agent card shows its identity, description, multiline prompt, timeout and result$`, func() error {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, "node", "node_modules/vitest/vitest.mjs", "run",
					"src/ui/messages/SpawnAgentCard.test.tsx", "--testNamePattern",
					"^spawn_agent displays agent identity, description, prompt and timeout$")
				if out, err := cmd.CombinedOutput(); err != nil {
					return fmt.Errorf("spawn agent DOM scenario: %w\n%s", err, out)
				}
				return nil
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/spawn_agent_card.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("spawn agent card feature failed")
	}
}
