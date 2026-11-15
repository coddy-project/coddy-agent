package llm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"syscall"
	"testing"
)

type kimiModelsFailingTransport struct{}

func (kimiModelsFailingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, syscall.ECONNRESET
}

func TestKimiModelsNetworkFailureIsRetryableAndRedacted(t *testing.T) {
	client := &http.Client{Transport: kimiModelsFailingTransport{}}
	_, err := fetchKimiModels(context.Background(), "subscription", "secret-token", "https://example.test/models?secret=secret-token", client)
	if err == nil || !errors.Is(err, syscall.ECONNRESET) || !isRetryableLLMError(err) {
		t.Fatalf("connection reset must be retryable: %v", err)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error exposes secret: %v", err)
	}
}
