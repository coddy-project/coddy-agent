package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	apptools "github.com/EvilFreelancer/coddy-agent/internal/tools"
)

type fakeSender struct {
	got *acp.QuestionRequestParams
}

func (f *fakeSender) SendSessionUpdate(string, interface{}) error { return nil }

func (f *fakeSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow"}, nil
}

func (f *fakeSender) RequestQuestion(_ context.Context, p acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	f.got = &p
	return &acp.QuestionResult{Answers: [][]string{{"A"}}}, nil
}

func TestQuestionToolPassesQuestionsAndToolCallID(t *testing.T) {
	s := &fakeSender{}
	env := &apptools.Env{
		CWD:        "/tmp",
		SessionID:  "sess-q",
		ToolCallID: "call-99",
		Sender:     s,
	}
	args := `{"questions":[{"question":"Pick","options":[{"label":"A"}]}]}`
	r := apptools.NewRegistry()
	out, err := r.Execute(context.Background(), "question", args, env)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if s.got == nil || !strings.Contains(s.got.RequestID, "q_") {
		t.Fatalf("request id: %#v", s.got)
	}
	if s.got.ToolCallID != "call-99" {
		t.Fatalf("toolCallId: %q", s.got.ToolCallID)
	}
	if s.got.SessionID != "sess-q" {
		t.Fatalf("session id: %q", s.got.SessionID)
	}
	var decoded acp.QuestionResult
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("result JSON: %v", err)
	}
	if len(decoded.Answers) != 1 || len(decoded.Answers[0]) != 1 || decoded.Answers[0][0] != "A" {
		t.Fatalf("answers: %#v", decoded.Answers)
	}
}

func TestPlanExitToolCallsSetMode(t *testing.T) {
	var mode string
	env := &apptools.Env{
		SetSessionMode: func(m string) error {
			mode = m
			return nil
		},
	}
	r := apptools.NewRegistry()
	_, err := r.Execute(context.Background(), "plan_exit", "{}", env)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if mode != "agent" {
		t.Fatalf("mode=%q", mode)
	}
}

func TestQuestionToolAcceptsASingleObjectInsteadOfAnArray(t *testing.T) {
	s := &fakeSender{}
	env := &apptools.Env{SessionID: "sess-q", Sender: s}
	args := `{"questions":{"question":"Solo","options":[{"label":"A"}]}}`
	r := apptools.NewRegistry()
	if _, err := r.Execute(context.Background(), "question", args, env); err != nil {
		t.Fatalf("single object form must be accepted: %v", err)
	}
	if s.got == nil || len(s.got.Questions) != 1 || s.got.Questions[0].Question != "Solo" {
		t.Fatalf("asked %#v", s.got)
	}
}

func TestQuestionToolAcceptsAStringEncodedSingleObject(t *testing.T) {
	s := &fakeSender{}
	env := &apptools.Env{SessionID: "sess-q", Sender: s}
	args := `{"questions":"{\"question\":\"Wrapped\",\"options\":[{\"label\":\"A\"}]}"}`
	r := apptools.NewRegistry()
	if _, err := r.Execute(context.Background(), "question", args, env); err != nil {
		t.Fatalf("string-encoded object form must be accepted: %v", err)
	}
	if s.got == nil || len(s.got.Questions) != 1 || s.got.Questions[0].Question != "Wrapped" {
		t.Fatalf("asked %#v", s.got)
	}
}

func TestQuestionToolRejectsGarbageQuestionsPayloads(t *testing.T) {
	env := &apptools.Env{SessionID: "sess-q", Sender: &fakeSender{}}
	r := apptools.NewRegistry()
	for _, args := range []string{
		`{"questions":42}`,
		`{"questions":"not json at all"}`,
		`{"questions":"\"deeply nested string\""}`,
	} {
		if _, err := r.Execute(context.Background(), "question", args, env); err == nil {
			t.Fatalf("args %s must be rejected", args)
		}
	}
}
