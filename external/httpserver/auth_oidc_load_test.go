//go:build http

package httpserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// TestOIDCLoopbackLoad measures a simultaneous burst through the HTTP stack,
// excluding login (provider work is separately throttled). Run by hand to
// avoid distorting the rest of the suite or overloading smaller CI workers.
func TestOIDCLoopbackLoad(t *testing.T) {
	if os.Getenv("CODDY_OIDC_LOAD_TEST") != "1" {
		t.Skip("set CODDY_OIDC_LOAD_TEST=1 to run the loopback load test")
	}
	for _, n := range []int{1, 100, 1000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f := newOIDCFixture(t)
			s := f.state.srv
			id := &oidc.IDToken{Issuer: f.provider.URL, Subject: "operator", Expiry: time.Now().Add(time.Hour)}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			requests := make([]*http.Request, n)
			for i := range requests {
				token, ok := benchIssueOIDC(s, authOpaque(), id)
				if !ok {
					t.Fatal("cannot populate active sessions")
				}
				r, err := http.NewRequestWithContext(ctx, "GET", f.state.ts.URL+"/v1/models", nil)
				if err != nil {
					t.Fatal(err)
				}
				r.Host = "coddy.example"
				r.AddCookie(&http.Cookie{Name: oidcSessionCookie, Value: token})
				requests[i] = r
			}
			latencies := make([]time.Duration, n)
			start := make(chan struct{})
			var wg sync.WaitGroup
			before := new(runtime.MemStats)
			runtime.ReadMemStats(before)
			for i, r := range requests {
				wg.Go(func() {
					<-start
					now := time.Now()
					resp, err := f.state.client.Do(r)
					if err != nil {
						t.Error(err)
						return
					}
					_, err = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					latencies[i] = time.Since(now)
					if err != nil || resp.StatusCode != 200 {
						t.Errorf("request %d: status %d, read error %v", i, resp.StatusCode, err)
					}
				})
			}
			close(start)
			wg.Wait()
			after := new(runtime.MemStats)
			runtime.ReadMemStats(after)
			slices.Sort(latencies)
			percentile := func(p int) time.Duration { return latencies[(p*n+99)/100-1] }
			t.Logf("n=%d p50=%v p95=%v p99=%v total allocated=%d bytes", n, percentile(50), percentile(95), percentile(99), after.TotalAlloc-before.TotalAlloc)
			if f.calls.Load() != 0 {
				t.Fatal("ordinary requests contacted issuer")
			}
		})
	}
}

func TestOIDCUnavailableProviderKeepsGateClosed(t *testing.T) {
	f := newOIDCFixture(t)
	f.provider.Close()
	preflight := f.request("GET", oidcStartPath, "")
	if preflight.Code != 303 || len(preflight.Result().Cookies()) != 1 {
		t.Fatalf("preflight: %d", preflight.Code)
	}
	if w := f.request("GET", preflight.Header().Get("Location"), "", preflight.Result().Cookies()...); w.Code != 503 || w.Header().Get("Location") != "" {
		t.Fatalf("unavailable issuer opened a flow: %d", w.Code)
	}
	if w := f.request("GET", "/coddy/sessions", ""); w.Code != 401 {
		t.Fatalf("unavailable issuer opened the gate: %d", w.Code)
	}
}
