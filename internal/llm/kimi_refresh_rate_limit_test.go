package llm

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestKimiRefreshRecoversAfterRateLimit(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi credential store unavailable")
	}
	store := NewKimiCredentialStore(privateKimiTestDir(t))
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	server, httpServer := newKimiRefreshServer(t, func(n int32, w http.ResponseWriter) {
		if n == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(w, `rate limited`)
			return
		}
		okReply(n, w)
	})
	ref := newTestRefresher(t, store, httpServer.URL, httpServer.Client())
	if _, err := ref.AccessToken(context.Background()); err == nil || !isRetryableLLMError(err) || retryDelayForError(err, 0, time.Second, 10*time.Second) != 7*time.Second {
		t.Fatalf("expected retryable rate limit with Retry-After, got %v", err)
	}
	if token, err := ref.AccessToken(context.Background()); err != nil || token != "a3" {
		t.Fatalf("token=%q error=%v", token, err)
	}
	if server.calls.Load() != 2 {
		t.Fatalf("calls=%d", server.calls.Load())
	}
}
