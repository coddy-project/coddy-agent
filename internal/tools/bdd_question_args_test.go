package tools_test

// Godog harness for features/question_lenient_args.feature: the question tool
// executes against the real registry with a stub sender (no LLM, no session);
// scenarios feed the argument encodings models actually produce and assert
// the user is asked and the answer round-trips.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	apptools "github.com/EvilFreelancer/coddy-agent/internal/tools"
)

type questionArgsState struct {
	sender *scriptedQuestionSender
	output string
	runErr error
}

type scriptedQuestionSender struct {
	answer string
	asked  *acp.QuestionRequestParams
}

func (s *scriptedQuestionSender) SendSessionUpdate(string, interface{}) error { return nil }

func (s *scriptedQuestionSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow"}, nil
}

func (s *scriptedQuestionSender) RequestQuestion(_ context.Context, p acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	s.asked = &p
	return &acp.QuestionResult{Answers: [][]string{{s.answer}}}, nil
}

func (s *questionArgsState) reset() {
	s.sender = nil
	s.output = ""
	s.runErr = nil
}

func (s *questionArgsState) aQuestionSenderThatAnswers(answer string) error {
	s.sender = &scriptedQuestionSender{answer: answer}
	return nil
}

func (s *questionArgsState) theQuestionToolRunsWithArguments(args *godog.DocString) error {
	env := &apptools.Env{
		CWD:        "/tmp",
		SessionID:  "sess-bdd-q",
		ToolCallID: "call-bdd-q",
		Sender:     s.sender,
	}
	r := apptools.NewRegistry()
	s.output, s.runErr = r.Execute(context.Background(), "question", args.Content, env)
	return nil
}

func (s *questionArgsState) theUserWasAsked(question string) error {
	if s.runErr != nil {
		return fmt.Errorf("question tool failed: %v", s.runErr)
	}
	if s.sender == nil || s.sender.asked == nil {
		return fmt.Errorf("the sender was never asked")
	}
	if len(s.sender.asked.Questions) == 0 || s.sender.asked.Questions[0].Question != question {
		return fmt.Errorf("asked %+v, want question %q", s.sender.asked.Questions, question)
	}
	return nil
}

func (s *questionArgsState) theToolReturnsTheAnswer(answer string) error {
	if s.runErr != nil {
		return fmt.Errorf("question tool failed: %v", s.runErr)
	}
	var decoded acp.QuestionResult
	if err := json.Unmarshal([]byte(s.output), &decoded); err != nil {
		return fmt.Errorf("result JSON: %w", err)
	}
	if len(decoded.Answers) != 1 || len(decoded.Answers[0]) != 1 || decoded.Answers[0][0] != answer {
		return fmt.Errorf("answers %#v, want %q", decoded.Answers, answer)
	}
	return nil
}

func initializeQuestionArgsScenario(sc *godog.ScenarioContext) {
	s := &questionArgsState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.Step(`^a question sender that answers "([^"]*)"$`, s.aQuestionSenderThatAnswers)
	sc.Step(`^the question tool runs with arguments:$`, s.theQuestionToolRunsWithArguments)
	sc.Step(`^the user was asked "([^"]*)"$`, s.theUserWasAsked)
	sc.Step(`^the tool returns the answer "([^"]*)"$`, s.theToolReturnsTheAnswer)
}

func TestQuestionLenientArgsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "question-lenient-args",
		ScenarioInitializer: initializeQuestionArgsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/question_lenient_args.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("question lenient args feature suite failed")
	}
}
