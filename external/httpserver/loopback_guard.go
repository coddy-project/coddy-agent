//go:build http

package httpserver

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// loopbackGuard narrows what a browser on another site can reach on a server
// that runs without a credential and takes the request on a loopback address,
// which is how `coddy serve` starts by default. Two requests are refused:
//
//   - one that names a host other than a loopback one (localhost, *.localhost,
//     127.0.0.0/8, ::1), because a local server is reached by those names and a
//     foreign name in Host means the browser thinks it talks to another site;
//   - a state-changing one (anything but GET, HEAD and OPTIONS) whose Origin is
//     neither the server's own nor one httpserver.cors admits.
//
// Clients that are not browsers send no Origin and name the address they dial,
// so they are not affected. With a credential configured (a token or the web
// sign-in) the auth gate decides instead, and a server reached on another
// interface is left to it as well: the guard is for the default deployment.
// The preview server keeps the same Host rule (internal/tools/preview).
func (s *Server) loopbackGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authPolicyNow().enabled || !arrivedOnLoopback(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !isLoopbackName(hostName(r.Host)) {
			http.Error(w, "this server answers only loopback host names while it runs without a credential; "+
				"set httpserver.auth_token or the web sign-in to reach it by another name", http.StatusMisdirectedRequest)
			return
		}
		if !isSafeMethod(r.Method) && !s.originAdmitted(r) {
			http.Error(w, "cross-origin requests that change state are refused while the server runs without a credential",
				http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// arrivedOnLoopback reports whether the connection the request came on was
// accepted on a loopback address. A request without that information (a
// handler called directly) is not a network request and is left alone.
func arrivedOnLoopback(r *http.Request) bool {
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok || addr == nil {
		return false
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// hostName is the host part of a Host header, lower-cased, without port or
// brackets.
func hostName(hostport string) string {
	h := strings.TrimSpace(hostport)
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}

// isLoopbackName reports whether a host name can only mean this machine.
func isLoopbackName(host string) bool {
	host = strings.TrimSuffix(host, ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// originAdmitted reports whether a state-changing request may come from its
// Origin: no Origin (not a browser, or a same-origin navigation), the server's
// own origin, or one httpserver.cors admits.
func (s *Server) originAdmitted(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if u, err := url.Parse(origin); err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if c := s.activeCfg(); c != nil {
		if _, ok := c.HTTPServer.CORSAllowOrigin(origin); ok {
			return true
		}
	}
	return false
}
