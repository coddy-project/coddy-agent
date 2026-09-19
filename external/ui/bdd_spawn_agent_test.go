//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// Run the DOM assertion in Vitest, where the actual React component is rendered.
// The http,ui test matrix installs these dependencies through make ui-build.
func TestSpawnAgentCardFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "spawn_agent_card",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^the spawn agent card shows its identity, description, multiline prompt, timeout and result$`, func() error {
				return runVitestScenario("src/ui/messages/SpawnAgentCard.test.tsx",
					"spawn_agent displays agent identity, description, prompt and timeout")
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
