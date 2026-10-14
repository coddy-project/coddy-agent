//go:build http && ui

package ui

import (
	"regexp"
	"testing"

	"github.com/cucumber/godog"
)

// Godog harness for features/toolsets_web_ui.feature: the Toolsets tab, the
// toolset of a logical model and the composer's toolset chip, each step a
// vitest scenario over the component it names.
func TestToolsetsWebUIFeature(t *testing.T) {
	steps := map[string]string{
		"the logical model form groups its fields: model, generation, reasoning, tools": "src/ui/settings/SettingsSection.test.tsx",
		"a model row picks its toolset from the presets, or none":                       "src/ui/settings/SettingsSection.test.tsx",
		"the Toolsets tab edits the lists of a preset in place":                         "src/ui/settings/SettingsSection.test.tsx",
		"renaming a preset renames it on the models that name it":                       "src/ui/settings/SettingsSection.test.tsx",
		"a toolset added in Settings starts with both lists empty":                      "src/ui/settings/SettingsSection.test.tsx",
		"the toolset of a model and the lists of a preset are named in Russian":         "src/ui/settings/SettingsSection.test.tsx",
		"the toolset chip follows the permission chip and switches the toolset":         "src/ui/chat/Composer.test.tsx",
		"the toolset chip appears once a reload brings a preset":                        "src/ui/App.toolset.test.tsx",
		"a toolset picked on the start screen rides in with the first message":          "src/ui/App.toolset.test.tsx",
	}
	suite := godog.TestSuite{
		Name: "toolsets_web_ui",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for step, file := range steps {
				step, file := step, file
				sc.Step("^"+regexp.QuoteMeta(step)+"$", func() error {
					return runVitestScenario(file, step)
				})
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/toolsets_web_ui.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("toolsets web UI feature failed")
	}
}
