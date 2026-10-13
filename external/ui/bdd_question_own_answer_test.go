//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// Issue #257: a question offers the reader an answer of their own unless it
// turns custom off. The card and the record of the question are rendered
// components, so each step runs the Vitest test that renders them.
func TestWebUIQuestionOwnAnswerFeature(t *testing.T) {
	steps := []struct{ step, file, name string }{
		{`^every question of the card offers an answer of the reader's own$`,
			"src/ui/chat/QuestionPromptSection.test.tsx",
			"every question offers an answer of the reader's own, single and multiple"},
		{`^a question of the card that turned custom off lists only its options$`,
			"src/ui/chat/QuestionPromptSection.test.tsx",
			"a question that turned custom off offers only its options"},
		{`^the record of a question keeps the free slot unless the question turned it off$`,
			"src/ui/messages/ToolCallMessage.test.tsx",
			"the record keeps the free slot unless the question turned it off"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_question_own_answer",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, s := range steps {
				file, name := s.file, s.name
				sc.Step(s.step, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_question_own_answer.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI question own answer feature failed")
	}
}
