package llm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/netx"
)

// The transport of a coddy row: HTTP/1.1 only, so that a swarm relay or a
// direct listener sees one TCP connection per shared call and can bound the
// life of a peer that vanished on it (the per-call user timeout exists only on
// an HTTP/1.x connection). Every other provider type keeps the shared
// transport with the HTTP/2 liveness pings.

// protoLog is what a TLS test remote saw of the requests it served.
type protoLog struct {
	mu       sync.Mutex
	requests []protoSeen
	offered  [][]string
}

type protoSeen struct {
	path       string
	protoMajor int
	negotiated string
}

func (l *protoLog) record(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	seen := protoSeen{path: r.URL.Path, protoMajor: r.ProtoMajor}
	if r.TLS != nil {
		seen.negotiated = r.TLS.NegotiatedProtocol
	}
	l.requests = append(l.requests, seen)
}

func (l *protoLog) snapshot() []protoSeen {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.requests)
}

func (l *protoLog) offers() [][]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.offered)
}

// newH2OfferingRemote is a TLS remote that offers h2 and http/1.1 in its ALPN,
// like a relay's listener, and answers the three routes a coddy row uses.
func newH2OfferingRemote(t *testing.T) (*httptest.Server, *protoLog) {
	t.Helper()
	log := &protoLog{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r)
		switch {
		case r.URL.Path == CoddyCompletionsPath:
			fw := startStream(w)
			fw.text("hi")
			fw.final(&Response{Content: "hi", StopReason: "end_turn", InputTokens: 1, OutputTokens: 1})
		case r.URL.Path == CoddyModelsPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"protocol":1,"data":[{"id":"coder","revision":"r1","max_context_tokens":1000}]}`))
		case strings.HasSuffix(r.URL.Path, "/usage"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"supported":true,"account_wide":true,"windows":[{"id":"session","label":"3h","used_percent":10}]}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-x"}]}`))
		}
	}))
	srv.EnableHTTP2 = true
	srv.TLS = &tls.Config{
		NextProtos: []string{"h2", "http/1.1"},
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			log.mu.Lock()
			log.offered = append(log.offered, slices.Clone(hello.SupportedProtos))
			log.mu.Unlock()
			return nil, nil
		},
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, log
}

// trustTestServer lets the shared transport the given provider type uses for
// the setting verify the test server's certificate, and puts the transport
// back when the test ends. It is the only way to reach a TLS test server
// through NewProvider, ListModels and the usage client, which build their own
// client from the row's proxy setting alone.
func trustTestServer(t *testing.T, srv *httptest.Server, providerType, setting string) {
	t.Helper()
	rt, err := providerTransportFor(providerType, setting)
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T", rt)
	}
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	prev := tr.TLSClientConfig
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	if prev != nil {
		cfg = prev.Clone()
		cfg.RootCAs = pool
	}
	tr.TLSClientConfig = cfg
	t.Cleanup(func() {
		tr.CloseIdleConnections()
		tr.TLSClientConfig = prev
	})
}

func TestCoddyRowsAreAnsweredOverHTTP1ByARemoteThatOffersH2(t *testing.T) {
	srv, log := newH2OfferingRemote(t)
	trustTestServer(t, srv, "coddy", "none")

	in := ProviderInput{Name: "remote", Type: "coddy", Model: "coder", BaseURL: srv.URL, APIKey: "tok", ProxyURL: "none", RetryDisabled: true}
	p, err := NewProvider(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Stream(context.Background(), userMsg("Hi"), nil, nil); err != nil {
		t.Fatalf("completion: %v", err)
	}
	if _, err := ListModels(context.Background(), in); err != nil {
		t.Fatalf("listing: %v", err)
	}
	if u, err := CoddyUsageForProvider(context.Background(), in, "coder"); err != nil || u == nil || !u.Supported {
		t.Fatalf("usage: %+v %v", u, err)
	}

	seen := log.snapshot()
	if len(seen) != 3 {
		t.Fatalf("the remote saw %d requests: %+v", len(seen), seen)
	}
	for _, s := range seen {
		if s.protoMajor != 1 || s.negotiated != "http/1.1" {
			t.Errorf("%s was served as HTTP/%d with ALPN %q, want HTTP/1.1", s.path, s.protoMajor, s.negotiated)
		}
	}
	for _, offered := range log.offers() {
		if !slices.Equal(offered, []string{"http/1.1"}) {
			t.Errorf("the ClientHello offered %v, want only http/1.1", offered)
		}
	}
}

// The control of the test above: the shared transport of every other provider
// type still negotiates h2 with the same server, so the harness cannot pass
// for the wrong reason.
func TestOtherProviderTypesStillNegotiateH2(t *testing.T) {
	srv, log := newH2OfferingRemote(t)
	for _, typ := range []string{"openai", "anthropic", "neuraldeep", "codex", "devin", ""} {
		rt, err := providerTransportFor(typ, "none")
		if err != nil {
			t.Fatal(err)
		}
		tr := rt.(*http.Transport)
		if tr.TLSNextProto["h2"] == nil {
			t.Errorf("type %q: the shared transport lost its HTTP/2 configuration (and with it the liveness pings)", typ)
		}
	}
	trustTestServer(t, srv, "openai", "none")
	in := ProviderInput{Name: "gpt", Type: "openai", BaseURL: srv.URL, APIKey: "tok", ProxyURL: "none"}
	if _, err := ListModels(context.Background(), in); err != nil {
		t.Fatalf("listing: %v", err)
	}
	seen := log.snapshot()
	if len(seen) != 1 || seen[0].protoMajor != 2 || seen[0].negotiated != "h2" {
		t.Fatalf("an openai row must keep HTTP/2: %+v", seen)
	}
	hc, err := HTTPClientForProviderProxy("none")
	if err != nil {
		t.Fatal(err)
	}
	if hc.Transport != mustTransport(t, "openai", "none") {
		t.Fatal("HTTPClientForProviderProxy no longer returns the shared transport")
	}
}

func mustTransport(t *testing.T, providerType, setting string) http.RoundTripper {
	t.Helper()
	rt, err := providerTransportFor(providerType, setting)
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

// Every provider type but coddy gets exactly the transport it had: the same
// object providerTransport hands out, for every setting.
func TestProviderTransportForKeepsEveryOtherTypeOnTheSharedTransport(t *testing.T) {
	settings := []string{"", "inherit", "none", "http://127.0.0.1:3128", "socks5://127.0.0.1:1080"}
	for _, typ := range []string{"openai", "anthropic", "neuraldeep", "codex", "devin", "", "something-else"} {
		for _, setting := range settings {
			want, err := providerTransport(setting)
			if err != nil {
				t.Fatal(err)
			}
			if got := mustTransport(t, typ, setting); got != want {
				t.Errorf("type %q setting %q: not the shared transport", typ, setting)
			}
		}
	}
}

func TestCoddyTransportIsHTTP1OnlyAndPinsItsALPN(t *testing.T) {
	for _, setting := range []string{"", "inherit", "none", "http://127.0.0.1:3128", "socks5://127.0.0.1:1080"} {
		rt := mustTransport(t, "coddy", setting)
		shared, err := providerTransport(setting)
		if err != nil {
			t.Fatal(err)
		}
		if rt == shared {
			t.Fatalf("setting %q: a coddy row shares the HTTP/2 transport", setting)
		}
		tr, ok := rt.(*http.Transport)
		if !ok {
			t.Fatalf("setting %q: transport is %T", setting, rt)
		}
		if tr.Protocols == nil || !tr.Protocols.HTTP1() || tr.Protocols.HTTP2() || tr.Protocols.UnencryptedHTTP2() {
			t.Errorf("setting %q: Protocols %v, want HTTP/1 only", setting, tr.Protocols)
		}
		if tr.ForceAttemptHTTP2 {
			t.Errorf("setting %q: ForceAttemptHTTP2 is set", setting)
		}
		if tr.TLSNextProto == nil || tr.TLSNextProto["h2"] != nil {
			t.Errorf("setting %q: the transport can still switch to h2: %v", setting, tr.TLSNextProto)
		}
		if tr.TLSClientConfig == nil || !slices.Equal(tr.TLSClientConfig.NextProtos, []string{"http/1.1"}) {
			t.Errorf("setting %q: ALPN is not pinned to http/1.1: %+v", setting, tr.TLSClientConfig)
		}
		// One transport per setting, kept for the process like the shared ones.
		if mustTransport(t, "coddy", setting) != rt {
			t.Errorf("setting %q: a second call built another transport", setting)
		}
	}
	if mustTransport(t, "coddy", "none") == mustTransport(t, "coddy", "") {
		t.Error("settings none and inherit share a coddy transport")
	}
}

// providers[].proxy applies to the HTTP/1.1 transport exactly as it does to the
// shared one.
func TestCoddyTransportCarriesTheRowsProxy(t *testing.T) {
	req := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u, Header: http.Header{}}
	}

	none := mustTransport(t, "coddy", "none").(*http.Transport)
	if none.Proxy != nil {
		t.Error("setting none must connect directly")
	}

	via := mustTransport(t, "coddy", "http://127.0.0.1:3128").(*http.Transport)
	got, err := via.Proxy(req("https://remote.example/x"))
	if err != nil || got == nil || got.Host != "127.0.0.1:3128" {
		t.Errorf("proxy URL setting: %v %v", got, err)
	}
	// A proxy URL is the whole route: a loopback target does not skip it.
	if got, _ := via.Proxy(req("http://127.0.0.1:9/x")); got == nil {
		t.Error("a loopback target skipped the row's proxy")
	}

	socks := mustTransport(t, "coddy", "socks5://127.0.0.1:1080").(*http.Transport)
	if socks.Proxy != nil || socks.DialContext == nil {
		t.Error("a socks proxy is the dialer's business")
	}

	inherit := mustTransport(t, "coddy", "").(*http.Transport)
	var asked []string
	f := func(r *http.Request) (*url.URL, error) {
		asked = append(asked, r.URL.String())
		return url.Parse("http://env-proxy.test:8080")
	}
	prev := environmentProxy.Swap(&f)
	t.Cleanup(func() { environmentProxy.Store(prev) })
	got, err = inherit.Proxy(req("https://remote.example/x"))
	if err != nil || got == nil || got.Host != "env-proxy.test:8080" || len(asked) != 1 {
		t.Errorf("inherit follows the environment's proxy per request: %v %v %v", got, err, asked)
	}
}

// A refused call must not upload the whole history: the transport still waits
// for the remote's verdict on Expect: 100-continue.
func TestCoddyTransportHoldsTheBodyBackForExpectContinue(t *testing.T) {
	for _, setting := range []string{"", "none", "http://127.0.0.1:3128"} {
		tr, ok := mustTransport(t, "coddy", setting).(*http.Transport)
		if !ok || tr.ExpectContinueTimeout <= 0 {
			t.Fatalf("setting %q: no ExpectContinueTimeout on the HTTP/1.1 transport", setting)
		}
		if tr.IdleConnTimeout <= 0 || tr.TLSHandshakeTimeout <= 0 {
			t.Errorf("setting %q: the clone of the default transport lost its timeouts", setting)
		}
	}
}

// The provider's HTTP client for a coddy row carries the stall guard and the
// request timeout over the HTTP/1.1 transport, like every other row over the
// shared one.
func TestProviderHTTPClientForACoddyRowIsBuiltOnItsTransport(t *testing.T) {
	hc, err := providerHTTPClientFor("coddy", "none", netx.ClientTLS{}, 5*time.Second, 7*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	guard, ok := hc.Transport.(*stallGuardTransport)
	if !ok || guard.idle != 7*time.Second || guard.inner != mustTransport(t, "coddy", "none") {
		t.Fatalf("client transport %T %+v", hc.Transport, hc.Transport)
	}
	if hc.Timeout != 5*time.Second {
		t.Errorf("timeout %v", hc.Timeout)
	}
	plain, err := providerHTTPClientFor("openai", "none", netx.ClientTLS{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Transport != mustTransport(t, "openai", "none") {
		t.Error("an openai row left the shared transport")
	}
	legacy, err := providerHTTPClient("none", 0, 0)
	if err != nil || legacy.Transport != plain.Transport {
		t.Errorf("providerHTTPClient changed: %v %v", legacy, err)
	}
}

func TestCoddyTransportRefusesABadProxySetting(t *testing.T) {
	for _, setting := range []string{"http://%zz", "ftp://127.0.0.1:21", "direct"} {
		if _, err := providerTransportFor("coddy", setting); err == nil {
			t.Errorf("setting %q was accepted", setting)
		}
	}
	// A bad setting leaves nothing cached behind.
	if _, err := HTTPClientForProviderProxy("direct"); err == nil {
		t.Error("the shared path accepts a bad setting")
	}
}
