//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// Issue #342: the composer's height, quotes from the transcript and where a
// conversation opens are DOM behaviour, asserted in Vitest where the React
// components render; each step names the Vitest test that holds it.
func TestLongPromptsAndReadingFeature(t *testing.T) {
	steps := []struct{ step, file, test string }{
		{`^the composer field grows with its text up to a ceiling and scrolls past it$`,
			"src/ui/chat/ComposerHeight.test.tsx",
			"the composer field grows with its text up to a ceiling and scrolls past it"},
		{`^the expand control gives the field the chat under its header, and a send folds it back$`,
			"src/ui/chat/ComposerHeight.test.tsx",
			"the expand control gives the field the chat under its header, and a send folds it back"},
		{`^the expand control stands under the jump to the newest message, which an expanded composer hides, and another chat opens folded$`,
			"src/ui/chat/ChatScreen.test.tsx",
			"the expand control stands under the jump to the newest message, which an expanded composer hides, and another chat opens folded"},
		{`^selecting text in an answer offers Quote, which adds it to the draft as a quote after what is already there$`,
			"src/ui/chat/ChatScreen.test.tsx",
			"selecting text in an answer offers Quote, which adds it to the draft as a quote after what is already there"},
		{`^a sent prompt shows its quotes as quotes$`,
			"src/ui/messages/UserMessage.test.tsx",
			"a sent prompt shows its quotes as quotes"},
		{`^a conversation opened after the reader scrolled up in another one lands on its newest message$`,
			"src/ui/chat/ChatScreen.test.tsx",
			"a conversation opened after the reader scrolled up in another one lands on its newest message"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_long_prompts_and_reading",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, s := range steps {
				sc.Step(s.step, func() error {
					return runVitestScenario(s.file, s.test)
				})
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_long_prompts_and_reading.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("long prompts and reading feature failed")
	}
}
