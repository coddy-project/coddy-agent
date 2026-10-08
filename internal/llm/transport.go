package llm

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The LLM transports: what every provider's HTTP client is built on.
//
// Two things distinguish them from http.DefaultTransport. HTTP/2 liveness:
// a connection that has carried no frame for http2ReadIdleTimeout is asked
// for a PING, and one that does not answer within http2PingTimeout is
// closed, so a tunnel or a proxy path that died without a FIN or a RST
// surfaces as "http2: client connection lost" (a retryable transport
// failure) instead of a request that waits forever - a proxy in the path
// (HTTPS_PROXY, providers[].proxy) answers TCP keepalives itself, and only
// an end-to-end frame proves the far side is there. And the stall guard:
// a streamed response body that sends nothing for StreamIdleTimeout after
// its first bytes is cut with a streamStalledError, because a model that
// stopped mid-answer looks exactly like one that is still thinking, and
// only a bound tells them apart. Rows of a provider type that is reached over
// HTTP/1.1 only (coddy) take neither the pings nor HTTP/2 itself, but the
// stall guard stays.

const (
	// http2ReadIdleTimeout is the silence on a connection after which the
	// transport sends a liveness PING.
	http2ReadIdleTimeout = 30 * time.Second
	// http2PingTimeout is how long that PING may go unanswered before the
	// connection is closed as lost.
	http2PingTimeout = 15 * time.Second
)

// enableHTTP2Liveness moves t to the x/net HTTP/2 transport, which is the
// same code net/http bundles, with ping health checks on: the standard
// library exposes no knob for them (net/http.Transport.HTTP2 has no effect
// yet, go.dev/issue/67813).
func enableHTTP2Liveness(t *http.Transport, readIdle, ping time.Duration) error {
	h2, err := http2.ConfigureTransports(t)
	if err != nil {
		return fmt.Errorf("http2 liveness: %w", err)
	}
	h2.ReadIdleTimeout = readIdle
	h2.PingTimeout = ping
	return nil
}

// streamStalledError reports a streamed response whose server sent nothing
// for idle after its first bytes. emitted mirrors streamTruncatedError:
// once deltas reached the caller a retry would stream the same text twice,
// so classification refuses it; before that the request is repeated like
// any other transport failure that delivered nothing.
type streamStalledError struct {
	idle time.Duration
}

func (e *streamStalledError) Error() string {
	// The duration never puts a space after its digits, so the status-code
	// scan in httpStatusFromError cannot mistake it for a code.
	return "stream stalled: no data from the model for " + e.idle.String()
}

// IsStreamStalled reports whether err carries a mid-response stall, so
// callers (the ReAct loop) can persist the partial answer the same way they
// do for a truncation and name the stall to the user.
func IsStreamStalled(err error) bool {
	var stalled *streamStalledError
	return errors.As(err, &stalled) || coddyStreamStalled(err)
}

// stallGuardTransport wraps the response body of every server-sent-events
// answer so a stream that goes silent after its first bytes is cut after
// idle. The request runs under a context of its own, cancelled on the
// stall, because cancellation is the one thing that unblocks a pending
// body read on both HTTP/1.1 and HTTP/2.
type stallGuardTransport struct {
	inner http.RoundTripper
	idle  time.Duration
}

func (t *stallGuardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	resp, err := t.inner.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	if !isEventStream(req, resp) {
		// A blocking answer arrives in one piece; nothing to guard, and the
		// context lives as long as the body does.
		resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
		return resp, nil
	}
	resp.Body = newIdleBody(resp.Body, t.idle, cancel)
	return resp, nil
}

// isEventStream reports whether resp is a server-sent-events answer: its
// Content-Type says so, or it is a success that names none and req asked for
// an event stream. The Codex backend answers its stream with no Content-Type
// at all, so the request is all that tells its body apart from a blocking
// answer; an error status is never a stream, whatever was asked for.
func isEventStream(req *http.Request, resp *http.Response) bool {
	if ct := strings.TrimSpace(resp.Header.Get("Content-Type")); ct != "" {
		return strings.HasPrefix(strings.ToLower(ct), "text/event-stream")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return false
	}
	return strings.Contains(strings.ToLower(req.Header.Get("Accept")), "text/event-stream")
}

// cancelOnClose releases the request context when the body is closed.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// idleBody is a response body with a stall timer: armed by the first bytes
// read and re-armed by every read after it, it cancels the request when it
// fires, and the read that fails as a result reports the stall instead of
// the cancellation.
type idleBody struct {
	rc     io.ReadCloser
	idle   time.Duration
	cancel context.CancelFunc

	mu      sync.Mutex
	timer   *time.Timer
	stalled bool
	closed  bool
}

func newIdleBody(rc io.ReadCloser, idle time.Duration, cancel context.CancelFunc) *idleBody {
	return &idleBody{rc: rc, idle: idle, cancel: cancel}
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > 0 {
		if b.stalled {
			// Bytes that raced the timer are still the server's, and the
			// stall is reported on the next read.
			return n, nil
		}
		b.arm()
		return n, err
	}
	if b.stalled {
		return 0, &streamStalledError{idle: b.idle}
	}
	return n, err
}

// arm starts or restarts the stall timer; callers hold mu.
func (b *idleBody) arm() {
	if b.closed {
		return
	}
	if b.timer == nil {
		b.timer = time.AfterFunc(b.idle, b.onStall)
		return
	}
	b.timer.Reset(b.idle)
}

func (b *idleBody) onStall() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.stalled = true
	b.mu.Unlock()
	// Cancelling the request is what unblocks the pending read.
	b.cancel()
}

func (b *idleBody) Close() error {
	b.mu.Lock()
	b.closed = true
	if b.timer != nil {
		b.timer.Stop()
	}
	b.mu.Unlock()
	err := b.rc.Close()
	b.cancel()
	return err
}

// Shared transports, one per proxy setting: a provider is built for every
// turn, and a transport of its own per turn would open a fresh TLS session
// for each request and keep the previous one idling. Every request a
// provider row makes - its completions, its model list, its account usage,
// a sign-in - goes through the transport of the row's setting, so the route
// is decided in this one place. A provider type that is reached over HTTP/1.1
// only (http1OnlyProviderType) has transports of its own, one per setting
// too, under keys the shared ones cannot collide with.
var (
	transportsMu sync.Mutex
	transports   = map[string]http.RoundTripper{}
)

// proxyFunc is the Proxy field of an http.Transport.
type proxyFunc = func(*http.Request) (*url.URL, error)

// environmentProxy, when set, stands in for the environment's proxy: net/http
// reads HTTPS_PROXY once per process and never applies it to a loopback
// address, so a test cannot stage it with the real variables. Unset, which
// it always is outside tests, a provider that inherits its proxy asks
// net/http.
var environmentProxy atomic.Pointer[proxyFunc]

// proxyFromEnvironment is the Proxy of the transport that inherits the
// environment's proxy. It asks per request, so a stand-in set after the
// shared transport was built still applies.
func proxyFromEnvironment(r *http.Request) (*url.URL, error) {
	if f := environmentProxy.Load(); f != nil {
		return (*f)(r)
	}
	return http.ProxyFromEnvironment(r)
}

// EnvironmentProxyFor names the proxy a provider that inherits its route uses
// for a request to target, the way its transport asks for it, or nil when
// that request goes direct (no variable set, NO_PROXY, a loopback address).
// Diagnostics use it so what they report is what the requests did.
func EnvironmentProxyFor(target *url.URL) (*url.URL, error) {
	return proxyFromEnvironment(&http.Request{URL: target, Header: http.Header{}})
}

// http1OnlyProviderType reports whether the rows of a provider type are
// reached over HTTP/1.1 only. A coddy row talks to a remote Coddy (or a swarm
// relay in front of one), and the remote bounds how long it keeps the slot of
// a peer that vanished with an option that exists only on a TCP connection
// that carries one call: an HTTP/2 connection multiplexes calls, so the option
// is never set on it. Offering HTTP/2 to such a remote would take that bound
// away. The cost is the liveness pings of HTTP/2: the heartbeat of the stream
// and the stall guard (StreamIdleTimeout) take their place.
func http1OnlyProviderType(providerType string) bool {
	return providerType == "coddy"
}

// http1TransportKeyPrefix keeps the HTTP/1.1 transports apart from the shared
// ones in the cache. No proxy URL starts with it (its scheme is followed by
// "://"), so the keys of every other provider type are exactly what they were.
const http1TransportKeyPrefix = "http/1.1|"

// providerTransport returns the shared transport for a providers[].proxy
// setting, read by config.ParseProxySetting: empty and "inherit" share the
// one that follows the environment's proxy, "none" has one that connects
// directly, and every proxy URL one of its own.
func providerTransport(setting string) (http.RoundTripper, error) {
	return providerTransportFor("", setting)
}

// providerTransportFor is providerTransport for the rows of one provider type:
// every type shares the transport of the setting, with HTTP/2 and its liveness
// pings, except the types http1OnlyProviderType names, which get a transport
// of their own with the same proxy rules and HTTP/1.1 only.
func providerTransportFor(providerType, setting string) (http.RoundTripper, error) {
	mode, proxyURL, err := config.ParseProxySetting(setting)
	if err != nil {
		return nil, err
	}
	var key string
	switch mode {
	case config.ProxyModeNone:
		key = config.ProxyNone
	case config.ProxyModeURL:
		key = proxyURL.String()
	}
	http1 := http1OnlyProviderType(providerType)
	if http1 {
		key = http1TransportKeyPrefix + key
	}
	transportsMu.Lock()
	defer transportsMu.Unlock()
	if t, ok := transports[key]; ok {
		return t, nil
	}
	var t *http.Transport
	if http1 {
		t, err = newHTTP1ProviderTransport(mode, proxyURL)
	} else {
		t, err = newProviderTransport(mode, proxyURL)
	}
	if err != nil {
		return nil, err
	}
	transports[key] = t
	return t, nil
}

// newBaseProviderTransport is a clone of the default transport routed by the
// row's proxy setting.
func newBaseProviderTransport(mode config.ProxyMode, proxyURL *url.URL) (*http.Transport, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default transport is not *http.Transport")
	}
	t := base.Clone()
	switch mode {
	case config.ProxyModeInherit:
		t.Proxy = proxyFromEnvironment
	case config.ProxyModeNone:
		// No Proxy function at all: nothing in the environment is read.
		t.Proxy = nil
	case config.ProxyModeURL:
		if err := routeThroughProxy(t, proxyURL); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func newProviderTransport(mode config.ProxyMode, proxyURL *url.URL) (*http.Transport, error) {
	t, err := newBaseProviderTransport(mode, proxyURL)
	if err != nil {
		return nil, err
	}
	if err := enableHTTP2Liveness(t, http2ReadIdleTimeout, http2PingTimeout); err != nil {
		return nil, err
	}
	return t, nil
}

// newHTTP1ProviderTransport is the transport of a row that is reached over
// HTTP/1.1 only. It is pinned three ways, so that none of them alone can be
// undone by a change elsewhere: Protocols names HTTP/1 and nothing else, the
// TLS next-protocol table is the empty one that disables HTTP/2 on every Go
// version, and the ALPN a ClientHello carries is exactly "http/1.1" (a server
// that offers h2 first is then answered over HTTP/1.1). HTTP/2 liveness is not
// configured: there is no HTTP/2 connection to ping.
func newHTTP1ProviderTransport(mode config.ProxyMode, proxyURL *url.URL) (*http.Transport, error) {
	t, err := newBaseProviderTransport(mode, proxyURL)
	if err != nil {
		return nil, err
	}
	t.ForceAttemptHTTP2 = false
	t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	t.Protocols = new(http.Protocols)
	t.Protocols.SetHTTP1(true)
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if t.TLSClientConfig != nil {
		cfg = t.TLSClientConfig.Clone()
	}
	cfg.NextProtos = []string{"http/1.1"}
	t.TLSClientConfig = cfg
	return t, nil
}

// providerHTTPClient is the client NewProvider hands the SDKs: the shared
// transport for the proxy setting, the stall guard when a stream idle
// timeout is set, and the request timeout when one is configured.
func providerHTTPClient(proxySetting string, timeout, streamIdle time.Duration) (*http.Client, error) {
	return providerHTTPClientFor("", proxySetting, timeout, streamIdle)
}

// providerHTTPClientFor is providerHTTPClient for the rows of one provider
// type (see providerTransportFor).
func providerHTTPClientFor(providerType, proxySetting string, timeout, streamIdle time.Duration) (*http.Client, error) {
	rt, err := providerTransportFor(providerType, proxySetting)
	if err != nil {
		return nil, err
	}
	if streamIdle > 0 {
		rt = &stallGuardTransport{inner: rt, idle: streamIdle}
	}
	return &http.Client{Transport: rt, Timeout: timeout}, nil
}
