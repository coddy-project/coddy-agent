// Package proxytest is a plain HTTP proxy for tests that name one in a proxy
// setting (providers[].proxy, gateways.telegram.proxy): it relays every
// absolute-form request to its target directly and records the paths it
// carried, so a request that went around it is one the proxy never saw.
// Plain-HTTP targets only: a CONNECT tunnel is refused.
package proxytest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
)

// hopByHop are the headers a proxy consumes rather than forwards: the
// connection-level ones and its own credentials.
var hopByHop = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Proxy-Connection", "TE", "Trailer", "Transfer-Encoding", "Upgrade",
}

// Proxy is a forwarding HTTP proxy on a loopback port.
type Proxy struct {
	srv    *httptest.Server
	direct *http.Transport

	mu    sync.Mutex
	paths []string
}

// New starts a proxy. Close it when the test ends.
func New() *Proxy {
	p := &Proxy{direct: &http.Transport{}}
	p.srv = httptest.NewServer(http.HandlerFunc(p.relay))
	return p
}

// URL is the proxy's address, the value a proxy setting takes.
func (p *Proxy) URL() string { return p.srv.URL }

// Carried lists the paths of the requests the proxy relayed, in order.
func (p *Proxy) Carried() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.paths)
}

// Close stops the proxy and drops its connections to the targets.
func (p *Proxy) Close() {
	p.srv.Close()
	p.direct.CloseIdleConnections()
}

func (p *Proxy) relay(w http.ResponseWriter, r *http.Request) {
	if !r.URL.IsAbs() {
		http.Error(w, "not a proxy request", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	p.paths = append(p.paths, r.URL.Path)
	p.mu.Unlock()
	out, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	out.Header = r.Header.Clone()
	// What a proxy consumes stays with it: its own credentials and the
	// hop-by-hop headers never reach the target.
	for _, h := range hopByHop {
		out.Header.Del(h)
	}
	out.ContentLength = r.ContentLength
	resp, err := p.direct.RoundTrip(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// WithoutProxyVariables drops the proxy variables from env, in either case,
// so a child process sees only the ones its test sets: net/http reads them
// once per process, so the real variables can only be staged in a child.
func WithoutProxyVariables(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "REQUEST_METHOD":
			continue
		}
		out = append(out, kv)
	}
	return out
}
