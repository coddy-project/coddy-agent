package llm

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAnthropicBlockingTimeout(t *testing.T) {
	cases := []struct {
		maxTokens int64
		want      time.Duration
	}{
		// The ten minutes the client gives any non-streaming request.
		{0, 10 * time.Minute},
		{8192, 10 * time.Minute},
		{21333, 10 * time.Minute},
		// Past them, the client's own estimate: an hour per 128000 tokens.
		{40000, 18*time.Minute + 45*time.Second},
		{64000, 30 * time.Minute},
		// Never more than an hour.
		{128000, time.Hour},
		{math.MaxInt64, time.Hour},
	}
	for _, tc := range cases {
		if got := anthropicBlockingTimeout(tc.maxTokens); got != tc.want {
			t.Errorf("anthropicBlockingTimeout(%d) = %s, want %s", tc.maxTokens, got, tc.want)
		}
	}
}

// Complete reads a stream nobody watches, so a stream cut midway has shown
// nothing to anyone and is as safe to retry as one that never started (the
// same cut under Stream, after visible text, is final:
// TestAnthropicStreamTruncatedKeepsPartial). Through the resilient wrapper
// NewProvider builds: the first stream is cut,
// the retry gets the whole answer, and the caller sees only that one.
func TestAnthropicCompleteRetriesACutStream(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, anthropicStreamPrefix)
			return
		}
		_, _ = io.WriteString(w, anthropicTextAnswerSSE("Whole answer."))
	}))
	defer srv.Close()
	p, err := NewProvider(ProviderInput{
		Type:          "anthropic",
		Model:         "claude-sonnet-4-5",
		APIKey:        "k",
		BaseURL:       srv.URL,
		MaxTokens:     40000,
		RetryMax:      1,
		RetryBase:     time.Millisecond,
		RetryMaxDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "Whole answer." || calls.Load() != 2 {
		t.Fatalf("content %q after %d request(s), want the whole answer after 2", resp.Content, calls.Load())
	}
}
