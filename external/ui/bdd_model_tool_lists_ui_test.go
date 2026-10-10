//go:build http && ui

package ui

import (
	"regexp"
	"testing"

	"github.com/cucumber/godog"
)

// Godog harness for features/model_tool_lists_web_ui.feature: the Tools block
// of the logical-model form, each step a vitest scenario over the component.
func TestModelToolListsWebUIFeature(t *testing.T) {
	const section = "src/ui/settings/SettingsSection.test.tsx"
	steps := []string{
		"the logical model form groups its fields: model, generation, reasoning, tools",
		"the tool lists of a model row are edited in place and saved on the row",
		"a model row without tool lists shows two empty lists and writes nothing until one is added to",
		"a model added in Settings starts with both tool lists empty",
		"the Tools block says how an entry is written and how the lists combine",
		"the tool lists and their block are named in Russian, with no English left over",
	}
	suite := godog.TestSuite{
		Name: "model_tool_lists_web_ui",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, step := range steps {
				sc.Step("^"+regexp.QuoteMeta(step)+"$", func() error {
					return runVitestScenario(section, step)
				})
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/model_tool_lists_web_ui.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("model tool lists web UI feature failed")
	}
}
