//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The summarizer pickers are React behaviour, so each step runs the Vitest
// test that renders the component and drives it.
func TestCompactionWebUIFeature(t *testing.T) {
	const settings = "src/ui/settings/SettingsSection.compaction.test.tsx"
	const composer = "src/ui/chat/Composer.commandArg.test.tsx"
	suite := godog.TestSuite{
		Name: "compaction_web_ui",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^picking a configured model as the summarizer in Settings sets compaction\.model and keeps the other compaction settings$`, func() error {
				return runVitestScenario(settings, "compaction offers configured models and preserves other settings when selecting")
			})
			sc.Step(`^after "/compact --model" the composer lists the configured models and narrows them as the id is typed$`, func() error {
				return runVitestScenario(composer, "/compact --model lists the configured models and narrows as the id is typed")
			})
			sc.Step(`^Enter puts the highlighted model into the draft instead of sending the command$`, func() error {
				return runVitestScenario(composer, "arrows move the highlight and Enter puts the model into the draft instead of sending")
			})
			sc.Step(`^two dashes after /compact offer the --model option, which opens the models$`, func() error {
				return runVitestScenario(composer, "two dashes offer --model, and picking it opens the models")
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/compaction_web_ui.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("compaction web UI feature failed")
	}
}
