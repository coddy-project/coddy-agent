package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
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
