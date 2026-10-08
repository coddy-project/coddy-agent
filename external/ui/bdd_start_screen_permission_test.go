//go:build http && ui

package ui

import (
	"regexp"
	"testing"

	"github.com/cucumber/godog"
)

// The start screen's permission chip and the pick it sends are App behaviour,
// asserted in Vitest against a stubbed server; each step names the test.
func TestStartScreenPermissionFeature(t *testing.T) {
	const file = "src/ui/App.permissionDefault.test.tsx"
	steps := []string{
		"the start screen's chip names the permission mode the server is configured with",
		"an explicit Ask first under a bypass configuration rides in with the first message",
		"a pick of another mode rides in with the first message",
	}
	suite := godog.TestSuite{
		Name: "web_ui_start_screen_permission",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, name := range steps {
				sc.Step("^"+regexp.QuoteMeta(name)+"$", func() error {
					return runVitestScenario(file, name)
				})
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_start_screen_permission.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("start screen permission feature failed")
	}
}
