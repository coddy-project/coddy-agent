package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type kimiFailingCloseBody struct{ io.Reader }

func (kimiFailingCloseBody) Close() error { return errors.New("synthetic close failure") }

func TestKimiInferenceDoesNotRetryAfterCloseFailure(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: kimiRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusUnauthorized, Body: kimiFailingCloseBody{Reader: strings.NewReader("")}}, nil
	})}
	ticket := &kimiCredentialTicket{token: "synthetic", kind: KimiSourceManaged}
	transport := &KimiInferenceTransport{endpoint: "https://example.test/chat/completions", client: client, catalogTicket: ticket, recovery: &KimiSubscriptionSource{}}
	if _, err := transport.Do(context.Background(), strings.NewReader(`{}`)); err == nil || !strings.Contains(err.Error(), "close rejected response") {
		t.Fatalf("close failure was not reported: %v", err)
	}
	if calls != 1 {
		t.Fatalf("inference attempts = %d, want 1", calls)
	}
}
