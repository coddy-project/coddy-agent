package llm

import (
	"context"
	"errors"
	"net"
	"net/http"
	"syscall"
	"testing"
)

type kimiRefreshDialFailure struct{}

func (kimiRefreshDialFailure) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
}

func TestKimiRefreshDialFailureKeepsCredential(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi credential store unavailable")
	}
	store := NewKimiCredentialStore(privateKimiTestDir(t))
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	ref := newTestRefresher(t, store, "https://example.test", &http.Client{Transport: kimiRefreshDialFailure{}})
	_, err := ref.AccessToken(context.Background())
	if err == nil || errors.Is(err, ErrKimiCredentialUncertain) || !isRetryableLLMError(err) {
		t.Fatalf("dial failure should be retryable, got %v", err)
	}
	_, state, loadErr := store.Load("global")
	if loadErr != nil || state != KimiCredentialValid {
		t.Fatalf("credential state=%v error=%v", state, loadErr)
	}
}
