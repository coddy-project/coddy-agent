package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func kimiResponsesFixture(t *testing.T, data string) *kimiResponsesProvider {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(data)) }))
	t.Cleanup(s.Close)
	return newKimiResponsesProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "model"})
}

func TestKimiResponsesReviewStatus(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("Retry-After", "7"); w.WriteHeader(429) }))
	defer s.Close()
	p := newKimiResponsesProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "model"})
	_, err := p.Complete(context.Background(), nil, nil)
	if d, ok := UpstreamRetryAfter(err); !ok || d != 7*time.Second || !isRetryableLLMError(err) {
		t.Fatalf("err=%v delay=%v ok=%v", err, d, ok)
	}
}
func TestKimiResponsesReviewStreamError(t *testing.T) {
	for _, prefix := range []string{"", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n"} {
		p := kimiResponsesFixture(t, prefix+"data: {\"type\":\"error\",\"error\":{\"code\":503,\"message\":\"busy\"}}\n\n")
		_, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {})
		if err == nil || !strings.Contains(err.Error(), "503") || isRetryableLLMError(err) != (prefix == "") {
			t.Fatalf("prefix=%q err=%v", prefix, err)
		}
	}
}
func TestKimiResponsesReviewArguments(t *testing.T) {
	for _, tc := range []struct {
		name, final string
		wantErr     bool
	}{{"suffix", "{}", false}, {"divergent", "[]", true}} {
		t.Run(tc.name, func(t *testing.T) {
			p := kimiResponsesFixture(t, "data: {\"type\":\"response.output_item.added\",\"output_index\":2,\"item\":{\"type\":\"function_call\",\"call_id\":\"c\",\"name\":\"f\"}}\n\n"+"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":2,\"delta\":\"{\"}\n\n"+"data: {\"type\":\"response.output_item.done\",\"output_index\":2,\"item\":{\"type\":\"function_call\",\"call_id\":\"c\",\"name\":\"f\",\"arguments\":\""+tc.final+"\"}}\n\n"+"data: {\"type\":\"response.completed\"}\n\n")
			var delta strings.Builder
			executed := false
			r, err := p.Stream(context.Background(), nil, nil, func(c StreamChunk) {
				if c.ToolCallDelta != nil {
					delta.WriteString(c.ToolCallDelta.InputJSON)
				}
				if c.ToolCall != nil {
					executed = true
				}
			})
			if tc.wantErr {
				if err == nil || executed || (r != nil && len(r.ToolCalls) > 0) {
					t.Fatalf("response=%#v err=%v", r, err)
				}
				return
			}
			if err != nil || r == nil || len(r.ToolCalls) != 1 || delta.String() != "{}" {
				t.Fatalf("response=%#v delta=%q err=%v", r, delta.String(), err)
			}
		})
	}
}
func TestKimiResponsesReviewMalformed(t *testing.T) {
	p := kimiResponsesFixture(t, "data: {\"type\":!}\n\n")
	_, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {})
	if err == nil || IsStreamTruncated(err) || isRetryableLLMError(err) {
		t.Fatalf("err=%v", err)
	}
}
func TestKimiResponsesReviewPartialUsage(t *testing.T) {
	p := kimiResponsesFixture(t, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"i\",\"call_id\":\"c\",\"name\":\"f\"}}\n\n"+"data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"i\",\"delta\":\"{\"}\n\n"+"data: {\"type\":\"response.in_progress\",\"response\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":2}}}\n\n")
	r, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {})
	if err == nil || r == nil || !r.UsageKnown || r.InputTokens != 5 || len(r.ToolCalls) != 0 {
		t.Fatalf("response=%#v err=%v", r, err)
	}
}
