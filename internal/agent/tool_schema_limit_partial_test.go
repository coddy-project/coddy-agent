package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestToolSchemaLimitKeepsPartialAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial answer\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"error\":{\"message\":\"tools.function.parameters: schema exceeds maximum allowed size\",\"type\":\"invalid_request_error\"}}\n\n")
	}))
	defer server.Close()
	p, err := llm.NewProvider(llm.ProviderInput{Type: "openai", Model: "fixture", APIKey: "fixture-key", BaseURL: server.URL, ProxyURL: "none", MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	assertPartialSchemaAnswer(t, p)
}

type partialSchemaProvider struct{ err error }

func (p *partialSchemaProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, p.err
}

func (p *partialSchemaProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	onChunk(llm.StreamChunk{TextDelta: "partial answer"})
	return nil, p.err
}

func TestToolSchemaLimitKeepsPartialAnswerWithoutProviderResponse(t *testing.T) {
	cause := errors.New("tools.function.parameters: schema exceeds maximum allowed size")
	assertPartialSchemaAnswer(t, &partialSchemaProvider{err: cause})
}

func assertPartialSchemaAnswer(t *testing.T, p llm.Provider) {
	t.Helper()
	ag := overflowRunAgent(t, p, 128000)
	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "hello"}})
	if err == nil || stop != string(acp.StopReasonRefused) || !llm.IsToolSchemaLimit(err) {
		t.Fatalf("stop=%q error=%v", stop, err)
	}
	messages := ag.state.GetMessages()
	if len(messages) == 0 || messages[len(messages)-1].Role != llm.RoleAssistant || messages[len(messages)-1].Content != "partial answer" {
		t.Fatalf("partial answer was not persisted: %#v", messages)
	}
	if strings.Contains(err.Error(), "partial answer") {
		t.Fatal("provider diagnostic leaked into the sanitized explanation")
	}
}
