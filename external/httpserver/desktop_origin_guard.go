//go:build http

package httpserver

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// DesktopOriginGuard protects a password-free desktop listener independently
// of the public API's CORS and allow_insecure settings. It runs before any
// proxy rewrites Host or Origin. The window is always served from loopback;
// other browser origins cannot use its implicit local authority.
func DesktopOriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, err := net.SplitHostPort(strings.TrimSpace(r.Host))
		if err != nil || port == "" || !desktopLoopbackHost(host) {
			http.Error(w, "the desktop answers only loopback host names", http.StatusMisdirectedRequest)
			return
		}
		if local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok && local != nil {
			if _, localPort, err := net.SplitHostPort(local.String()); err != nil || localPort != port {
				http.Error(w, "the desktop host does not match its listener", http.StatusMisdirectedRequest)
				return
			}
		}

		origin := strings.TrimSpace(r.Header.Get("Origin"))
		canvas := isCanvasFilePath(r.URL.Path)
		extension := desktopBrowserExtension(r.URL.Path, origin)
		if origin != "" && (origin != "null" || !canvas) && !extension && !desktopSameOrigin(origin, r.Host, r.TLS != nil) {
			http.Error(w, "cross-origin requests to the desktop are refused", http.StatusForbidden)
			return
		}
		site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
		if !canvas && !extension && (site == "cross-site" || site == "same-site") {
			http.Error(w, "cross-origin requests to the desktop are refused", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// The browser extension authenticates its own WebSocket and file tickets.
// Keep its endpoint reachable without widening CORS for the rest of the API.
func desktopBrowserExtension(path, origin string) bool {
	if !strings.HasPrefix(path, browserEndpointPrefix) {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return u.Scheme == "chrome-extension" || u.Scheme == "moz-extension"
}

func desktopLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func desktopSameOrigin(origin, host string, tls bool) bool {
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	scheme := "http"
	if tls {
		scheme = "https"
	}
	return strings.EqualFold(u.Scheme, scheme) && strings.EqualFold(u.Host, host)
}
