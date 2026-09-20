//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// What a preview card does is React behaviour, so each step runs the Vitest
// test that renders the surface and clicks the card.
func TestComposerImagePreviewFeature(t *testing.T) {
	const composer = "src/ui/chat/Composer.test.tsx"
	const bubble = "src/ui/messages/UserMessage.test.tsx"
	suite := godog.TestSuite{
		Name: "composer_image_preview",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^an image in the composer is a preview card that opens the picture enlarged$`, func() error {
				return runVitestScenario(composer,
					"an image attachment is a preview card that opens the picture enlarged")
			})
			sc.Step(`^removing a preview card in the composer drops the attachment and opens nothing$`, func() error {
				return runVitestScenario(composer,
					"removing a preview card drops the attachment and opens nothing")
			})
			sc.Step(`^an image in the sent bubble opens the full-size asset$`, func() error {
				return runVitestScenario(bubble,
					"an image in the sent bubble opens the full-size asset enlarged")
			})
			sc.Step(`^a bubble sent before the full-size asset existed opens its preview instead$`, func() error {
				return runVitestScenario(bubble,
					"a bubble that predates the full-size url falls back to the preview")
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/composer_image_preview.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("composer image preview feature failed")
	}
}
