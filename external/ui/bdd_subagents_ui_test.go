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
			sc.Step(`^the Subagents settings tab approves a definition for the session workspace and shows it as trusted$`, func() error {
				return runVitestScenario("src/ui/settings/SubagentsSection.test.tsx",
					"approving posts the workspace and reloads the catalog")
			})
			sc.Step(`^the refused spawn_agent notice approves the definition without starting it again$`, func() error {
				return runVitestScenario("src/ui/messages/SubagentApprovalNotice.test.tsx",
					"approving posts the workspace and never retries the spawn")
			})
			sc.Step(`^the Tasks panel answers a detached subagent's prompt against the child session$`, func() error {
				return runVitestScenario("src/ui/tasks/BackgroundTasksPanel.test.tsx",
					"a detached subagent's prompt is answered on its task card")
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
