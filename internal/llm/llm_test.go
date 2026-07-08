package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWrappedStreamCancelIsCanceled(t *testing.T) {
	inner := context.Canceled
	wrapped := fmt.Errorf("openai stream: %w", inner)
	if !errors.Is(wrapped, context.Canceled) {
		t.Fatal("agent must detect cancel when provider wraps stream.Err with fmt.Errorf")
	}
}

// TestOpenAIMultimodalMessageContentParts verifies that a user Message with
// ImageParts is serialised as an array of content parts (text + image_url)
// rather than a plain string.
func TestOpenAIMultimodalMessageContentParts(t *testing.T) {
	p := newOpenAIProvider("gpt-4o", "key", "", nil, 1024, 0.0, "")
	msgs := []Message{
		{Role: RoleUser, Content: "describe this", ImageParts: []ImagePart{
			{DataURL: "data:image/png;base64,abc123", Name: "test.png"},
		}},
	}
	params := p.buildParams(msgs, nil)
	raw, err := json.Marshal(params.Messages)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, `"image_url"`) {
		t.Errorf("expected image_url content part, got: %s", s)
	}
	if !strings.Contains(s, `data:image/png;base64,abc123`) {
		t.Errorf("expected base64 data URL, got: %s", s)
	}
	if !strings.Contains(s, `"describe this"`) {
		t.Errorf("expected text content, got: %s", s)
	}
}

// TestNewProviderAnthropicHonorsBaseURL verifies that an Anthropic provider built
// through NewProvider routes requests to the configured api_base (BaseURL) instead of
// the hard-coded https://api.anthropic.com default. Regression test: BaseURL used to be
// dropped on the Anthropic branch, so OpenAI-compatible api_base overrides were ignored.
func TestNewProviderAnthropicHonorsBaseURL(t *testing.T) {
	var mu sync.Mutex
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hit = true
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-test",`+
			`"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",`+
			`"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()

	prov, err := NewProvider(ProviderInput{
		Type:    "anthropic",
		Model:   "claude-test",
		APIKey:  "test-key",
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := prov.Complete(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !hit {
		t.Fatal("Anthropic provider ignored api_base: request did not reach the configured BaseURL server")
	}
	if resp.Content != "ok" {
		t.Errorf("unexpected content %q, want %q", resp.Content, "ok")
	}
}

func TestProviderBaseURLNeuralDeepIsFixed(t *testing.T) {
	if got := providerBaseURL("neuraldeep", "https://example.invalid/v1"); got != neuralDeepBaseURL {
		t.Fatalf("providerBaseURL(neuraldeep) = %q, want %q", got, neuralDeepBaseURL)
	}
}

func TestNewProviderNeuralDeepIsSupported(t *testing.T) {
	if _, err := NewProvider(ProviderInput{
		Type:    "neuraldeep",
		Model:   "default",
		APIKey:  "nd-test-key",
		BaseURL: "https://example.invalid/v1",
	}); err != nil {
		t.Fatalf("NewProvider(neuraldeep): %v", err)
	}
}

// TestOpenAITextOnlyMessageIsString verifies that a user Message without
// ImageParts still results in a plain string content field.
func TestOpenAITextOnlyMessageIsString(t *testing.T) {
	p := newOpenAIProvider("gpt-4o", "key", "", nil, 1024, 0.0, "")
	msgs := []Message{
		{Role: RoleUser, Content: "hello"},
	}
	params := p.buildParams(msgs, nil)
	raw, err := json.Marshal(params.Messages)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	if strings.Contains(s, `"image_url"`) {
		t.Errorf("unexpected image_url in text-only message: %s", s)
	}
	if !strings.Contains(s, `"hello"`) {
		t.Errorf("expected text content, got: %s", s)
	}
}
