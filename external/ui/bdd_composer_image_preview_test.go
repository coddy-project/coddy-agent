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
	const copies = "src/ui/chat/Composer.attachmentCopy.test.tsx"
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
			sc.Step(`^a dropped file is replaced by a copy held in memory$`, func() error {
				return runVitestScenario(copies, "a dropped file is replaced by a copy held in memory")
			})
			sc.Step(`^files dropped one after another keep their order whichever is read first$`, func() error {
				return runVitestScenario(copies, "files dropped one after another keep their order whichever is read first")
			})
			sc.Step(`^a file that cannot be read leaves the others and says which it was$`, func() error {
				return runVitestScenario(copies, "a file that cannot be read leaves the others and says which it was")
			})
			sc.Step(`^the row of a queued picture opens it from the session's assets at once$`, func() error {
				return runVitestScenario("src/ui/chat/queuedUserMessage.test.ts",
					"a picture read with a known session opens from the session's assets at once")
			})
			sc.Step(`^a server file with nothing to show yet keeps the picture the row has$`, func() error {
				return runVitestScenario("src/ui/chat/transcriptServerSnapshot.test.ts",
					"preserveUserMessageFiles keeps a local picture for a server file that has nothing to show yet")
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
