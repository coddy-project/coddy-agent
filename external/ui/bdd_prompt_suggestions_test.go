//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestWebUIPromptSuggestionsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "web-ui-prompt-suggestions",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^editing and clearing the composer preserves the suggestion for "(Tab|ArrowRight)"$`, func(key string) error {
				return runVitestScenario("src/ui/chat/Composer.test.tsx", "the composer preserves its suggestion through editing and clearing - "+key)
			})
		},
		Options: &godog.Options{
			Format: "pretty", Paths: []string{"../../features/web_ui_prompt_suggestions.feature"}, TestingT: t, Strict: true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI prompt suggestion scenarios failed")
	}
}
