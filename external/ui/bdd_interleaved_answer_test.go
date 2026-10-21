//go:build http && ui

package ui

import (
	"regexp"
	"testing"

	"github.com/cucumber/godog"
)

// Godog harness for features/web_ui_interleaved_answer.feature: the end of a
// turn whose answer came in parts, each step a vitest scenario over the stream
// consumer and the check the end of a turn makes.
func TestInterleavedAnswerFeature(t *testing.T) {
	const stream = "src/ui/chat/consumeComposerSse.order.test.ts"
	const probe = "src/ui/chat/streamSyncLocalAssistant.test.ts"
	steps := map[string][2]string{
		"a turn whose text and reasoning interleave records every segment of its answer":    {stream, "a turn whose text and reasoning interleave records every segment of its answer"},
		"a turn whose merged answer carries the id of its first segment counts as answered": {probe, "the end of a turn whose answer came in segments a turn whose merged answer carries the id of its first segment counts as answered"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_interleaved_answer",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for step, target := range steps {
				file, name := target[0], target[1]
				sc.Step("^"+regexp.QuoteMeta(step)+"$", func() error {
					return runVitestScenario(file, name)
				})
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_interleaved_answer.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("interleaved answer feature failed")
	}
}
