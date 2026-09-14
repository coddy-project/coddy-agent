//go:build http && ui

package ui

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// runVitestScenario runs one named Vitest test, where the React component is
// actually rendered against a stubbed API. The http,ui test matrix installs
// the dependencies through make ui-build.
func runVitestScenario(file, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "node_modules/vitest/vitest.mjs", "run",
		file, "--testNamePattern", "^"+regexp.QuoteMeta(name)+"$")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w\n%s", name, err, out)
	}
	return nil
}

func TestSubagentsWebUIFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "subagents_web_ui",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^the Subagents settings tab lists the definitions of the session workspace with their scope, description and file$`, func() error {
				return runVitestScenario("src/ui/settings/SubagentsSection.test.tsx",
					"lists every definition of the session workspace with its scope, description and file")
			})
			sc.Step(`^a background subagent's prompt waits at the end of its parent chat$`, func() error {
				return runVitestScenario("src/ui/chat/ChatScreen.test.tsx",
					"a background subagent's prompt waits at the end of its parent chat")
			})
			sc.Step(`^the parent chat answers that prompt against the child session$`, func() error {
				return runVitestScenario("src/ui/chat/SubagentPermissionCard.test.tsx",
					"a background subagent's prompt is answered in the parent chat against the child session")
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/subagents_web_ui.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("subagents web UI feature failed")
	}
}
