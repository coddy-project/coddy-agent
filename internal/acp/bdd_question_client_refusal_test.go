package acp_test

// Godog harness for features/question_client_refusal.feature (issue #760):
// the question tool runs against a real ACP server wired to a scripted client,
// so a client that refuses session/request_question is observed the way the
// model gets it - as the tool's error, not as an empty set of answers.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	apptools "github.com/EvilFreelancer/coddy-agent/internal/tools"
)

type questionClientRefusalState struct {
	t      *testing.T
	wire   *wireServer
	done   chan questionToolOutcome
	output string
	runErr error
}

type questionToolOutcome struct {
	out string
	err error
}

func (s *questionClientRefusalState) reset() {
	s.wire = nil
	s.done = nil
	s.output = ""
	s.runErr = nil
}

func (s *questionClientRefusalState) aClientWithoutElicitation() error {
	s.wire = startWireServer(s.t, &mockHandler{})
	s.wire.initialize(s.t, `{}`)
	return nil
}

// theQuestionToolAsks runs the real question tool with the wire server as its
// sender, so the client's answer decides what the model gets back.
func (s *questionClientRefusalState) theQuestionToolAsks(question string) error {
	if s.wire == nil {
		return fmt.Errorf("no client connected")
	}
	args, err := json.Marshal(map[string]interface{}{
		"questions": []map[string]interface{}{{
			"question": question,
			"options":  []map[string]string{{"label": "JSON"}, {"label": "YAML"}},
		}},
	})
	if err != nil {
		return err
	}
	s.done = make(chan questionToolOutcome, 1)
	go func() {
		env := &apptools.Env{CWD: "/tmp", SessionID: "s1", ToolCallID: "call-q", Sender: s.wire.srv}
		out, err := apptools.NewRegistry().Execute(context.Background(), "question", string(args), env)
		s.done <- questionToolOutcome{out: out, err: err}
	}()
	return nil
}

// questionRequestID finds the id the server put on its session/request_question.
func (s *questionClientRefusalState) questionRequestID() (json.RawMessage, error) {
	var id json.RawMessage
	scanner := bufio.NewScanner(strings.NewReader(s.wire.out.String()))
	for scanner.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) == nil && msg.Method == "session/request_question" {
			id = msg.ID
		}
	}
	if id == nil {
		return nil, fmt.Errorf("no request id on the wire:\n%s", s.wire.out.String())
	}
	return id, nil
}

func (s *questionClientRefusalState) theClientRefusesTheQuestion(code int, message string) error {
	s.wire.awaitWritten(s.t, `"session/request_question"`)
	id, err := s.questionRequestID()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return s.wire.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":%d,"message":%s}}`, id, code, raw))
}

func (s *questionClientRefusalState) theClientAnswers(answer string) error {
	s.wire.awaitWritten(s.t, `"session/request_question"`)
	id, err := s.questionRequestID()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(answer)
	if err != nil {
		return err
	}
	return s.wire.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"answers":[[%s]]}}`, id, raw))
}

// outcome waits for the tool run the scenario started.
func (s *questionClientRefusalState) outcome() error {
	if s.done == nil {
		return fmt.Errorf("the question tool was never run")
	}
	select {
	case got := <-s.done:
		s.output, s.runErr = got.out, got.err
		s.done = nil
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("the question tool never finished")
	}
}

func (s *questionClientRefusalState) theToolReportsAnErrorNaming(text string) error {
	if err := s.outcome(); err != nil {
		return err
	}
	if s.runErr == nil {
		return fmt.Errorf("the question tool answered %q, want an error naming %q", s.output, text)
	}
	if !strings.Contains(s.runErr.Error(), text) {
		return fmt.Errorf("error = %q, want it to name %q", s.runErr.Error(), text)
	}
	return nil
}

func (s *questionClientRefusalState) theToolReturnsTheAnswer(answer string) error {
	if err := s.outcome(); err != nil {
		return err
	}
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

func initializeQuestionClientRefusalScenario(t *testing.T, sc *godog.ScenarioContext) {
	s := &questionClientRefusalState{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})

	sc.Step(`^an ACP client that does not advertise elicitation$`, s.aClientWithoutElicitation)
	sc.Step(`^the question tool asks "([^"]*)"$`, s.theQuestionToolAsks)
	sc.Step(`^the client refuses the question with error (-?\d+) "([^"]*)"$`, s.theClientRefusesTheQuestion)
	sc.Step(`^the client answers "([^"]*)"$`, s.theClientAnswers)
	sc.Step(`^the question tool reports an error naming "([^"]*)"$`, s.theToolReportsAnErrorNaming)
	sc.Step(`^the question tool returns the answer "([^"]*)"$`, s.theToolReturnsTheAnswer)
}

func TestQuestionClientRefusalFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "question-client-refusal",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			initializeQuestionClientRefusalScenario(t, sc)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/question_client_refusal.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("question client refusal feature suite failed")
	}
}
