//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestTranscriptHeadingsWebUIFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "web_ui_transcript_headings",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^prompts and replies have localized article labels and preserve Markdown heading levels$`, func() error {
				for _, locale := range []string{"en", "ru"} {
					if err := runVitestScenario("src/ui/messages/TranscriptHeadings.test.tsx", "messages keep named articles and preserve Markdown heading outline in "+locale); err != nil {
						return err
					}
				}
				return nil
			})
		},
		Options: &godog.Options{
			Format: "pretty", Paths: []string{"../../features/web_ui_transcript_headings.feature"},
			TestingT: t, Strict: true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("transcript heading navigation web UI feature failed")
	}
}
