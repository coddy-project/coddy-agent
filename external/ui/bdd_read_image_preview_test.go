//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The preview of a picture a read showed the model, or an MCP tool returned,
// is React behaviour, so each step runs the Vitest test that feeds the frame,
// maps the transcript or renders the row.
func TestReadImagePreviewFeature(t *testing.T) {
	const stream = "src/ui/chat/consumeComposerSse.order.test.ts"
	const transcript = "src/ui/chat/transcriptFromMessages.test.ts"
	const row = "src/ui/messages/ToolCallMessage.test.tsx"
	suite := godog.TestSuite{
		Name: "read_image_preview",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a completed read keeps the picture its call showed the model while the turn streams$`, func() error {
				return runVitestScenario(stream, "a completed read keeps the pictures its call showed the model")
			})
			sc.Step(`^a reloaded transcript keeps the picture on the read's row$`, func() error {
				return runVitestScenario(transcript, "a tool result carries the pictures its call showed the model")
			})
			sc.Step(`^the read's row previews the picture without being opened and opens the original enlarged$`, func() error {
				return runVitestScenario(row, "a read that showed the model a picture previews it under the row")
			})
			sc.Step(`^a completed MCP call keeps the screenshot its tool returned while the turn streams$`, func() error {
				return runVitestScenario(stream, "a completed MCP call keeps the screenshot its tool returned")
			})
			sc.Step(`^a reloaded transcript keeps the screenshot on the MCP call's row$`, func() error {
				return runVitestScenario(transcript, "an MCP tool row keeps the screenshot its tool returned")
			})
			sc.Step(`^the MCP card previews the screenshot without being opened and opens the original enlarged$`, func() error {
				return runVitestScenario(row, "an MCP card previews the screenshot its tool returned under the card")
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/read_image_preview.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("read image preview feature failed")
	}
}
