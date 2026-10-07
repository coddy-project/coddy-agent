package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/cucumber/godog"
)

type anthropicEmptyAssistantState struct {
	mu       sync.Mutex
	provider *anthropicProvider
	server   *httptest.Server
	messages []any
	calls    int
}

func (s *anthropicEmptyAssistantState) givenConversation() error {
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.calls++
		s.messages = request.Messages
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"test-model","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	s.provider = newAnthropicProvider("test-model", "key", s.server.URL, nil, 8192, 0, "high")
	return nil
}

func (s *anthropicEmptyAssistantState) sendNextRequest() error {
	_, err := s.provider.Complete(context.Background(), []Message{
		{Role: RoleUser, Content: "first"},
		{Role: RoleAssistant, ReasoningSignature: "sig"},
		{Role: RoleUser, Content: "second"},
	}, nil)
	return err
}

func (s *anthropicEmptyAssistantState) requestHasOnlyUserMessages() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) != 2 {
		return fmt.Errorf("request has %d messages, want 2", len(s.messages))
	}
	for i, want := range []string{"first", "second"} {
		message, ok := s.messages[i].(map[string]any)
		if !ok || message["role"] != "user" {
			return fmt.Errorf("message %d is not a user message: %#v", i, s.messages[i])
		}
		blocks, ok := message["content"].([]any)
		if !ok || len(blocks) != 1 {
			return fmt.Errorf("message %d content = %#v, want %q", i, message["content"], want)
		}
		block, ok := blocks[0].(map[string]any)
		if !ok || block["text"] != want || block["type"] != "text" {
			return fmt.Errorf("message %d content = %#v, want %q", i, message["content"], want)
		}
	}
	return nil
}

func (s *anthropicEmptyAssistantState) exactlyOneRequest() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls != 1 {
		return fmt.Errorf("provider calls = %d, want 1", s.calls)
	}
	return nil
}

func TestAnthropicEmptyAssistantFeature(t *testing.T) {
	s := &anthropicEmptyAssistantState{}
	suite := godog.TestSuite{
		Name: "anthropic-empty-assistant",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				if s.server != nil {
					s.server.Close()
				}
				return ctx, nil
			})
			sc.Step(`^an Anthropic conversation contains a signature-only assistant turn$`, s.givenConversation)
			sc.Step(`^Coddy sends the next request$`, s.sendNextRequest)
			sc.Step(`^the request contains the surrounding user messages without an assistant block$`, s.requestHasOnlyUserMessages)
			sc.Step(`^exactly one request reaches the provider$`, s.exactlyOneRequest)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/anthropic_empty_assistant.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("Anthropic empty assistant feature suite failed")
	}
}
