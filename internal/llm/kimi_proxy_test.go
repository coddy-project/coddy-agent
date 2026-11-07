package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKimiDeviceLoginUsesProviderProxy(t *testing.T) {
	for _, tc := range []struct{ region, host string }{
		{"global", "auth.kimi.ai:443"},
		{"china", "auth.kimi.com:443"},
	} {
		t.Run(tc.region, func(t *testing.T) {
			requests := make(chan string, 1)
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.Method + " " + r.Host
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer proxy.Close()
			if _, err := StartKimiDeviceLogin(context.Background(), tc.region, proxy.URL); err == nil {
				t.Fatal("expected proxy rejection")
			}
			select {
			case got := <-requests:
				if want := "CONNECT " + tc.host; got != want {
					t.Fatalf("proxy request = %q, want %q", got, want)
				}
			default:
				t.Fatal("provider proxy received no request")
			}
		})
	}
}
