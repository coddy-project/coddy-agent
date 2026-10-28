//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestExtensionStreamFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "ui_extension_stream",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^enabled extensions transform incoming text and tool data on fresh and resumed streams$`, func() error {
				for _, transport := range []string{"post", "relay"} {
					if err := runVitestScenario("src/ui/chat/consumeComposerSse.extensions.test.ts", "enabled extensions transform incoming text and tool data on "+transport); err != nil {
						return err
					}
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/ui_extension_stream.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("extension stream feature failed")
	}
}
