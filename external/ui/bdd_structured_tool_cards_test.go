//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestStructuredToolCardsFeature(t *testing.T) {
	const testFile = "src/ui/messages/StructuredToolCard.test.tsx"
	suite := godog.TestSuite{
		Name: "structured_tool_cards",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^the model switch card shows the model, reasoning and lifetime$`, func() error {
				return runVitestScenario(testFile, "switch_model names the choice and lifetime")
			})
			sc.Step(`^the HTTP card separates the request and response and masks credentials$`, func() error {
				return runVitestScenario(testFile, "http_request separates response status, headers and body and masks credentials")
			})
			sc.Step(`^the background task card lists task ids and states$`, func() error {
				return runVitestScenario(testFile, "background_list separates each task's id, state and detail")
			})
			sc.Step(`^the preview server card links to its address$`, func() error {
				return runVitestScenario(testFile, "preview_server exposes its address as a link")
			})
			sc.Step(`^the documentation search card lists matching sections$`, func() error {
				return runVitestScenario(testFile, "documentation search displays references as rows")
			})
			sc.Step(`^the plan cards show the list, document and saved identity$`, func() error {
				return runVitestScenario(testFile, "plan list, read and write have plan-specific bodies")
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/structured_tool_cards.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("structured tool cards feature failed")
	}
}
