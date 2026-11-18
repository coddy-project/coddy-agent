//go:build http

package httpserver

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDesktopOriginGuard(t *testing.T) {
	for _, tc := range []struct {
		name, host, origin, site, path string
		want                           int
	}{
		{"direct loopback", "127.0.0.1:49152", "", "none", "/v1/models", http.StatusNoContent},
		{"same origin", "127.0.0.1:49152", "http://127.0.0.1:49152", "same-origin", "/v1/models", http.StatusNoContent},
		{"localhost", "localhost:49152", "http://localhost:49152", "same-origin", "/v1/models", http.StatusNoContent},
		{"DNS rebinding", "attacker.example:49152", "http://attacker.example:49152", "same-origin", "/v1/models", http.StatusMisdirectedRequest},
		{"wrong port", "127.0.0.1:49153", "", "none", "/v1/models", http.StatusMisdirectedRequest},
		{"other loopback origin", "127.0.0.1:49152", "http://127.0.0.1:49153", "same-site", "/v1/models", http.StatusForbidden},
		{"foreign origin", "127.0.0.1:49152", "https://attacker.example", "cross-site", "/v1/models", http.StatusForbidden},
		{"cross-site without origin", "127.0.0.1:49152", "", "cross-site", "/v1/models", http.StatusForbidden},
		{"opaque API origin", "127.0.0.1:49152", "null", "cross-site", "/v1/models", http.StatusForbidden},
		{"opaque canvas capability", "127.0.0.1:49152", "null", "cross-site", "/coddy/canvas/capability/index.html", http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:49152"+tc.path, nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 49152}))
			res := httptest.NewRecorder()
			DesktopOriginGuard(next).ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("status = %d, want %d", res.Code, tc.want)
			}
		})
	}
}
