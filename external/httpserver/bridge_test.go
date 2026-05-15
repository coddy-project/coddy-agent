//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestForwardTextChunk_ReasoningEmittedAsReasoningContent(t *testing.T) {
	rec := httptest.NewRecorder()
	sender := NewSender(&config.Config{}, rec, true, "agent-model")
	err := sender.SendSessionUpdate("sess-x", acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeReasoning, Text: "silent plan"},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := rec.Body.String()
	if !strings.Contains(raw, `"reasoning_content":"silent plan"`) {
		t.Fatalf("expected reasoning_content in SSE body, got: %s", raw)
	}
	if strings.Contains(raw, `"content":"silent plan"`) {
		t.Fatalf("reasoning must not map to delta.content, got: %s", raw)
	}
	var payload map[string]interface{}
	idx := strings.Index(raw, "{")
	if idx < 0 {
		t.Fatal("no json in response")
	}
	jsonLine := raw[idx:]
	if nl := strings.IndexByte(jsonLine, '\n'); nl >= 0 {
		jsonLine = jsonLine[:nl]
	}
	if err := json.Unmarshal([]byte(jsonLine), &payload); err != nil {
		t.Fatal(err)
	}
	choices, _ := payload["choices"].([]interface{})
	ch0 := choices[0].(map[string]interface{})
	delta := ch0["delta"].(map[string]interface{})
	if delta["reasoning_content"] != "silent plan" {
		t.Fatalf("delta: %#v", delta)
	}
	if _, has := delta["content"]; has {
		t.Fatalf("reasoning chunk should omit content field, delta=%#v", delta)
	}
}

func TestRequestQuestionSSECompletesWhenPosted(t *testing.T) {
	rec := httptest.NewRecorder()
	sender := NewSender(&config.Config{}, rec, true, "agent-model")
	ctx := context.Background()
	p := acp.QuestionRequestParams{
		SessionID: "s1",
		RequestID: "r1",
		Questions: []acp.QuestionPrompt{{Question: "x", Options: []acp.QuestionOption{{Label: "y"}}}},
	}
	done := make(chan error, 1)
	var got *acp.QuestionResult
	go func() {
		r, err := sender.RequestQuestion(ctx, p)
		if err != nil {
			done <- err
			return
		}
		got = r
		done <- nil
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.Body.String(), "event: question") {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if ok := CompleteQuestionAnswer("s1", "r1", &acp.QuestionResult{Answers: [][]string{{"y"}}}); !ok {
		t.Fatal("CompleteQuestionAnswer failed")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Answers) != 1 || len(got.Answers[0]) != 1 || got.Answers[0][0] != "y" {
		t.Fatalf("unexpected result %#v", got)
	}
}

func TestForwardTextChunk_TextUsesContentDelta(t *testing.T) {
	rec := httptest.NewRecorder()
	sender := NewSender(&config.Config{}, rec, true, "agent-model")
	err := sender.SendSessionUpdate("sess-x", acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := rec.Body.String()
	if !strings.Contains(raw, `"content":"hello"`) {
		t.Fatalf("expected content in SSE body, got: %s", raw)
	}
	if strings.Contains(raw, "reasoning_content") {
		t.Fatalf("text chunk must not set reasoning_content, got: %s", raw)
	}
}
