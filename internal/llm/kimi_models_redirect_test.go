package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestKimiModelsRefusesSameOriginRedirect(t *testing.T) {
	var redirected atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			http.Redirect(w, r, "/other", http.StatusFound)
			return
		}
		redirected.Store(true)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	_, err := fetchKimiModels(context.Background(), "subscription", "synthetic-token", server.URL+"/models", server.Client())
	if err == nil || redirected.Load() {
		t.Fatalf("same-origin redirect followed: redirected=%v error=%v", redirected.Load(), err)
	}
}
