//go:build http

package httpserver

import (
	"net"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// browserEndpointPrefix is the extension's endpoint (internal to external/browser):
// a chrome-extension:// page calls it, and it asks for the extension's key
// itself, so it stays outside the guard as it stays outside the auth gate.
const browserEndpointPrefix = "/browser/v1/"

// openServerGuard narrows what a browser on another site can reach on a server
// that runs without a credential, which is how `coddy serve` starts by default.
// Two requests are refused:
//
//   - one that arrived on a loopback address and names another host than a
//     loopback one (localhost, *.localhost, 127.0.0.0/8, ::1): a local server is
//     reached by those names, and a foreign name in Host means the browser thinks
//     it talks to another site, as under DNS rebinding;
//   - a state-changing one (anything but GET, HEAD and OPTIONS) whose Origin is
//     neither the server's own nor one httpserver.cors admits, on any address: a
//     port Docker publishes reaches the server on the container's own address.
//
// Clients that are not browsers send no Origin and name the address they dial,
// so they are not affected. With a credential configured (a token or the web
// sign-in) the auth gate decides instead, and httpserver.allow_insecure says the
// server is open on purpose: a stand on the LAN, or a proxy in front that signs
// people in by itself. The preview server keeps the same Host rule
// (internal/tools/preview).
func (s *Server) openServerGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authPolicyNow().enabled || s.openOnPurpose() || strings.HasPrefix(r.URL.Path, browserEndpointPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		if arrivedOnLoopback(r) && !isLocalHostName(hostName(r.Host)) {
			http.Error(w, "this server answers only loopback host names while it runs without a credential; "+
				"set httpserver.auth_token or the web sign-in to reach it by another name, "+
				"or httpserver.allow_insecure if it is open on purpose", http.StatusMisdirectedRequest)
			return
		}
		if isStateChanging(r) && !s.originAdmitted(r) {
			http.Error(w, "cross-origin requests that change state are refused while the server runs without a credential; "+
				"list the origin in httpserver.cors, set a credential, or httpserver.allow_insecure if it is open on purpose",
				http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) openOnPurpose() bool {
	c := s.activeCfg()
	return c != nil && c.HTTPServer.AllowInsecure
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

// isLocalHostName reports whether a host name can only mean this machine, or
// the tunnel of the relay this node dialled, whose name no browser can resolve.
func isLocalHostName(host string) bool {
	host = strings.TrimSuffix(host, ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, swarm.TunnelHostSuffix) {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// originAdmitted reports whether a state-changing request may come from its
// Origin: no Origin (not a browser), the server's own origin, or one
// httpserver.cors admits.
func (s *Server) originAdmitted(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" || originMatchesHost(origin, r.Host) {
		return true
	}
	if c := s.activeCfg(); c != nil {
		if _, ok := c.HTTPServer.CORSAllowOrigin(origin); ok {
			return true
		}
	}
	return false
}
