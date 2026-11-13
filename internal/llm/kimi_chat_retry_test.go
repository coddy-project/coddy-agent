package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestKimiChatPreservesRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	transport := &KimiInferenceTransport{endpoint: server.URL, client: server.Client(), source: func(context.Context) (string, error) { return "token", nil }}
	provider := newKimiChatProvider(transport, ProviderInput{Model: "kimi"})
	_, err := provider.Complete(context.Background(), nil, nil)
	if got := httpStatusFromError(err); got != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; error: %v", got, err)
	}
	if delay, ok := serverRetryDelay(err); !ok || delay != 120*time.Second {
		t.Fatalf("retry delay = %v, present = %v; error: %v", delay, ok, err)
	}
}
