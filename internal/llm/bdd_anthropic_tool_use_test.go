package llm

// Godog harness for features/anthropic_tool_use_after_text.feature: the real
// Anthropic provider, through the SDK client, against a stub server that
// streams a Messages API answer whose tool calls come after text or thinking.
// The stream builders are the ones of anthropic_tool_use_index_test.go.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/cucumber/godog"
)

type anthropicToolUseState struct {
	t      *testing.T
	blocks [][]string
	// next is the index of the next content block of the answer.
	next int
	// toolIDs are the ids of the tool_use blocks, in the order they were written.
	toolIDs []string
	server  *httptest.Server
	seen    *streamedCalls
	resp    *Response
}

func (s *anthropicToolUseState) reset() {
	s.blocks, s.next, s.toolIDs, s.seen, s.resp = nil, 0, nil, nil, nil
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
}

func (s *anthropicToolUseState) answerSays(text string) error {
	s.blocks = append(s.blocks, anthropicTextBlock(s.next, text))
	s.next++
	return nil
}

func (s *anthropicToolUseState) answerThinks(thinking string) error {
	s.blocks = append(s.blocks, anthropicThinkingBlock(s.next, thinking, "sig"))
	s.next++
	return nil
}

func (s *anthropicToolUseState) answerCalls(name, arguments string) error {
	id := fmt.Sprintf("toolu_%d", len(s.toolIDs)+1)
	s.toolIDs = append(s.toolIDs, id)
	s.blocks = append(s.blocks, anthropicToolUseBlock(s.next, id, name, arguments))
	s.next++
	return nil
}

func (s *anthropicToolUseState) streamAnswer() error {
	sse := anthropicSSE(s.t, append(anthropicEvents(s.blocks...), anthropicMessageEnd("tool_use")...)...)
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	p := newAnthropicProvider("test-model", "key", s.server.URL, nil, 0, 0, "")
	seen, onChunk := collectToolCalls()
	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, onChunk)
	if err != nil {
		return fmt.Errorf("stream failed: %w", err)
	}
	s.seen, s.resp = seen, resp
	return nil
}

func (s *anthropicToolUseState) answerKeepsText(text string) error {
	if s.resp.Content != text {
		return fmt.Errorf("text = %q, want %q", s.resp.Content, text)
	}
	return nil
}

func (s *anthropicToolUseState) answerKeepsThinking(thinking string) error {
	if s.resp.Reasoning != thinking {
		return fmt.Errorf("thinking = %q, want %q", s.resp.Reasoning, thinking)
	}
	return nil
}

func (s *anthropicToolUseState) answerCarriesToolCalls(table *godog.Table) error {
	var want []ToolCall
	for i, row := range table.Rows[1:] {
		want = append(want, ToolCall{ID: s.toolIDs[i], Name: row.Cells[0].Value, InputJSON: row.Cells[1].Value})
	}
	if !reflect.DeepEqual(s.resp.ToolCalls, want) {
		return fmt.Errorf("tool calls = %+v, want %+v", s.resp.ToolCalls, want)
	}
	if s.resp.StopReason != "tool_use" {
		return fmt.Errorf("stop reason = %q, want tool_use", s.resp.StopReason)
	}
	return nil
}

func (s *anthropicToolUseState) everyAnnouncedCallIsDelivered() error {
	if len(s.seen.announced) != len(s.toolIDs) || !reflect.DeepEqual(s.seen.announced, s.toolIDs) {
		return fmt.Errorf("announced = %v, want %v", s.seen.announced, s.toolIDs)
	}
	var delivered []string
	for _, c := range s.seen.completed {
		delivered = append(delivered, c.ID)
	}
	if !reflect.DeepEqual(delivered, s.seen.announced) {
		return fmt.Errorf("delivered = %v, announced %v: a pending row would never close", delivered, s.seen.announced)
	}
	return nil
}

func TestAnthropicToolUseAfterTextFeature(t *testing.T) {
	s := &anthropicToolUseState{t: t}
	suite := godog.TestSuite{
		Name: "anthropic-tool-use-after-text",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				s.reset()
				return ctx, nil
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				s.reset()
				return ctx, nil
			})
			sc.Step(`^an Anthropic answer that says "([^"]*)"$`, s.answerSays)
			sc.Step(`^an Anthropic answer that thinks "([^"]*)"$`, s.answerThinks)
			sc.Step(`^the answer then calls "([^"]*)" with (.+)$`, s.answerCalls)
			sc.Step(`^Coddy streams the answer$`, s.streamAnswer)
			sc.Step(`^the answer keeps the text "([^"]*)"$`, s.answerKeepsText)
			sc.Step(`^the answer keeps the thinking "([^"]*)"$`, s.answerKeepsThinking)
			sc.Step(`^the answer carries these tool calls in order$`, s.answerCarriesToolCalls)
			sc.Step(`^every announced tool call is delivered$`, s.everyAnnouncedCallIsDelivered)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/anthropic_tool_use_after_text.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("Anthropic tool use after text feature suite failed")
	}
}
