//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestUserMessageSpacingWebUIFeature(t *testing.T) {
	steps := []struct{ step, file, test string }{
		{`^follow-up spacing follows the global user index rather than the visible row position$`,
			"src/ui/messages/MessageList.userSpacing.test.tsx",
			"a read-only window keeps the global follow-up spacing without edit controls"},
		{`^a follow-up prompt keeps one gap above it and its footer room below$`,
			"src/ui/chat/userFootCss.test.ts",
			"a follow-up prompt keeps one gap above it and its footer room below"},
		{`^the user foot keeps Copy before circular Edit and time, with Undo first when offered$`,
			"src/ui/messages/UserMessage.test.tsx",
			"the user foot keeps Copy before circular Edit and time, with Undo true"},
	}
	suite := godog.TestSuite{
		Name: "user_message_spacing_web_ui",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, s := range steps {
				sc.Step(s.step, func() error { return runVitestScenario(s.file, s.test) })
			}
		},
		Options: &godog.Options{
			Format: "pretty", Paths: []string{"../../features/user_message_spacing_web_ui.feature"},
			TestingT: t, Strict: true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("user message spacing web UI feature failed")
	}
}
