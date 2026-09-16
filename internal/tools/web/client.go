package web

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// userAgent is what Coddy calls itself on the wire unless a request says otherwise.
const userAgent = "coddy-agent/1.0 (+https://github.com/EvilFreelancer/coddy-agent)"

// maxRedirects bounds a chain of followed redirects, the same ceiling net/http uses.
const maxRedirects = 10

// transferPolicy is how one exchange is carried out. http_request and webfetch
// build their requests the same way and differ only here: webfetch follows every
// redirect and vets each address it is sent to, http_request follows only the
// redirects that stay on the origin the operator approved and sends exactly the
// headers it was given.
type transferPolicy struct {
	// timeout bounds the whole exchange, reading the body included.
	timeout time.Duration
	// followRedirects follows 3xx answers; off, the 3xx answer is the result.
	followRedirects bool
	// sameOriginOnly holds a redirect that leaves the request's origin (an
	// http to https upgrade on the same host excepted) instead of following it.
	sameOriginOnly bool
	// guard vets every address before it is contacted: the first one and each
	// redirect. Nil contacts any address.
	guard func(ctx context.Context, u *url.URL) error
	// decompress lets the transport ask for gzip and undo it. Off, the request
	// carries only the Accept-Encoding its caller set, and the body arrives as
	// the server encoded it.
	decompress bool
	// route picks the proxy and the certificate check.
	route route
}

// route is how a request reaches its destination: through the proxies the
// environment names (the zero value), through one proxy, or directly; and
// whether the server certificate is verified.
type route struct {
	// proxy is the proxy every request goes through; nil defers to the
	// environment unless direct is set.
	proxy *url.URL
	// direct ignores the environment's proxies.
	direct bool
	// insecure skips the TLS certificate check, like curl -k.
	insecure bool
}

func (r route) key(decompress bool) string {
	proxy := ""
	if r.proxy != nil {
		proxy = r.proxy.String()
	}
	return fmt.Sprintf("%t|%t|%t|%s", decompress, r.direct, r.insecure, proxy)
}

// transfer is one exchange whose response body is still unread.
type transfer struct {
	resp *http.Response
	// followed lists the addresses of the redirects that were followed.
	followed []string
	// held is the address of a redirect that was not followed because it
	// leaves the origin; the response is that redirect.
	held   *url.URL
	cancel context.CancelFunc
}

// Close releases the response body and the exchange's deadline.
func (t *transfer) Close() {
	if t == nil {
		return
	}
	if t.resp != nil && t.resp.Body != nil {
		_ = t.resp.Body.Close()
	}
	if t.cancel != nil {
		t.cancel()
	}
}

// maxCachedTransports bounds the transport cache. A model that walks through
// many proxies must not pin a connection pool per proxy for the life of the
// process, so a full cache is dropped and its idle connections closed.
const maxCachedTransports = 16

var (
	transportsMu sync.Mutex
	transports   = map[string]*http.Transport{}
)

// transportFor returns a transport shared by every exchange with the same
// route, so connections are reused across calls. Each is a clone of the default
// transport: HTTP/2, dial and idle timeouts, and - unless the route says
// otherwise - the proxies named by the environment.
func transportFor(decompress bool, r route) http.RoundTripper {
	key := r.key(decompress)
	transportsMu.Lock()
	defer transportsMu.Unlock()
	if t, ok := transports[key]; ok {
		return t
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	if len(transports) >= maxCachedTransports {
		for k, t := range transports {
			t.CloseIdleConnections()
			delete(transports, k)
		}
	}
	t := base.Clone()
	t.DisableCompression = !decompress
	switch {
	case r.proxy != nil:
		t.Proxy = http.ProxyURL(r.proxy)
	case r.direct:
		t.Proxy = nil
	}
	if r.insecure {
		cfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if t.TLSClientConfig != nil {
			cfg = t.TLSClientConfig.Clone()
		}
		cfg.InsecureSkipVerify = true // #nosec G402 -- the caller asked for it and the operator approved it
		t.TLSClientConfig = cfg
	}
	transports[key] = t
	return t
}

// send carries out one exchange under the policy. The caller reads the body
// and closes the transfer.
func send(ctx context.Context, req *http.Request, p transferPolicy) (*transfer, error) {
	if p.guard != nil {
		if err := p.guard(ctx, req.URL); err != nil {
			return nil, err
		}
	}
	var cancel context.CancelFunc
	if p.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	req = req.WithContext(ctx)
	tr := &transfer{cancel: cancel}
	origin := req.URL
	client := &http.Client{
		Transport: transportFor(p.decompress, p.route),
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if !p.followRedirects {
				return http.ErrUseLastResponse
			}
			if p.sameOriginOnly && !sameOriginOrUpgrade(origin, next.URL) {
				tr.held = next.URL
				return http.ErrUseLastResponse
			}
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			if p.guard != nil {
				if err := p.guard(next.Context(), next.URL); err != nil {
					return err
				}
			}
			tr.followed = append(tr.followed, next.URL.String())
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		if errors.Is(err, context.DeadlineExceeded) && p.timeout > 0 {
			return nil, fmt.Errorf("no answer within %s: %w", p.timeout, err)
		}
		return nil, err
	}
	tr.resp = resp
	return tr, nil
}

// readLimited reads at most limit bytes and reports whether there was more.
func readLimited(r io.Reader, limit int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return data, false, err
	}
	if int64(len(data)) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

// originOf renders scheme://host[:port] with the host lower-cased and the
// scheme's default port left out, so two spellings of one origin compare equal.
func originOf(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" && port != defaultPort(scheme) {
		host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	return scheme + "://" + host
}

func defaultPort(scheme string) string {
	switch strings.ToLower(scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func portOrDefault(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	return defaultPort(u.Scheme)
}

// sameOriginOrUpgrade reports whether a redirect from a to b stays on a's
// origin. An http address upgraded to https on the same host and default ports
// counts as staying: it is where every plain-http API sends a client first.
func sameOriginOrUpgrade(a, b *url.URL) bool {
	if originOf(a) == originOf(b) {
		return true
	}
	return strings.EqualFold(a.Scheme, "http") && strings.EqualFold(b.Scheme, "https") &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		portOrDefault(a) == "80" && portOrDefault(b) == "443"
}
