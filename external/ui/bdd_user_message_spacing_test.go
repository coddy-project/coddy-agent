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
		{`^follow-up prompts balance the footer room with shared spacing variables$`,
			"src/ui/chat/userFootCss.test.ts",
			"follow-up prompts balance the footer room with shared spacing variables"},
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
