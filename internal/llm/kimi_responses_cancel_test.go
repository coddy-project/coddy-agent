package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type kimiResponsesRoundTrip func(*http.Request) (*http.Response, error)

func (f kimiResponsesRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type kimiResponsesCancelAfterTerminal struct {
	context.Context
	cancel    context.CancelFunc
	delivered bool
}

func (c *kimiResponsesCancelAfterTerminal) Err() error {
	if c.delivered {
		c.cancel()
	}
	return c.Context.Err()
}

type kimiResponsesTerminalReader struct {
	io.Reader
	ctx *kimiResponsesCancelAfterTerminal
}

func (r *kimiResponsesTerminalReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.ctx.delivered = true
	}
	return n, err
}

func TestKimiResponsesCancelAfterTerminal(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &kimiResponsesCancelAfterTerminal{Context: base, cancel: cancel}
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":4,\"output_tokens\":2}}}\n\n"
	client := &http.Client{Transport: kimiResponsesRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(&kimiResponsesTerminalReader{Reader: strings.NewReader(body), ctx: ctx})}, nil
	})}
	p := newKimiResponsesProvider(&KimiInferenceTransport{endpoint: "https://example.test", client: client, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "model"})
	var text strings.Builder
	r, err := p.Stream(ctx, nil, nil, func(c StreamChunk) { text.WriteString(c.TextDelta) })
	if err != nil || r == nil || r.Content != "hello" || text.String() != "hello" || !r.UsageKnown || r.InputTokens != 4 || r.StopReason != "end_turn" {
		t.Fatalf("response=%#v delta=%q err=%v", r, text.String(), err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("context was not cancelled")
	}
}

func TestKimiResponsesCancelBeforeTerminalKeepsDelta(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
	client := &http.Client{Transport: kimiResponsesRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	p := newKimiResponsesProvider(&KimiInferenceTransport{endpoint: "https://example.test", client: client, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "model"})
	r, err := p.Stream(ctx, nil, nil, func(c StreamChunk) {
		if c.TextDelta != "" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || r == nil || r.Content != "partial" || isRetryableLLMError(err) {
		t.Fatalf("response=%#v err=%v", r, err)
	}
}
