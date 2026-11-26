//go:build http

package httpserver

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const oidcNetworkTimeout = 10 * time.Second
const oidcMaxResponse = 256 << 10

var errOIDCProvider = errors.New("external identity provider unavailable")

type oidcProvider struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
}

func newExternalAuth() externalAuth {
	return externalAuth{machineScope: capabilityScope{authOpaque(), authOpaque()}, slots: make(chan struct{}, 16), client: newOIDCHTTPClient()}
}

// The global budget bounds provider traffic even when the caller rotates
// addresses or the provider answers too quickly for the concurrency cap.
type oidcBudget struct {
	mu        sync.Mutex
	last      time.Time
	available float64
}

func (b *oidcBudget) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.available = min(64, b.available+32*now.Sub(b.last).Seconds())
	b.last = now
	if b.available < 1 {
		return false
	}
	b.available--
	return true
}

func (a *externalAuth) takeSlot() bool {
	select {
	case a.slots <- struct{}{}:
		if a.budget.allow() {
			return true
		}
		a.releaseSlot()
	default:
	}
	return false
}
func (a *externalAuth) releaseSlot() { <-a.slots }

func (a *externalAuth) discover(ctx context.Context, o oidcSettings) (*oidcProvider, error) {
	a.discoveryMu.Lock()
	defer a.discoveryMu.Unlock()
	if a.discoverySettings == o {
		if a.provider != nil {
			return a.provider, nil
		}
		if time.Now().Before(a.discoveryRetry) {
			return nil, errOIDCProvider
		}
	}
	if ctx.Err() != nil {
		return nil, errOIDCProvider
	}
	a.discoverySettings = o
	a.provider = nil
	a.discoveryRetry = time.Now().Add(oidcNetworkTimeout + 2*time.Second)
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, a.client), o.issuer)
	if err != nil {
		return nil, errOIDCProvider
	}
	endpoint := p.Endpoint()
	var metadata struct {
		JWK         string   `json:"jwks_uri"`
		AuthMethods []string `json:"token_endpoint_auth_methods_supported"`
	}
	if p.Claims(&metadata) != nil || oidcHTTPS(endpoint.AuthURL) == nil || oidcHTTPS(endpoint.TokenURL) == nil || oidcHTTPS(metadata.JWK) == nil {
		return nil, errOIDCProvider
	}
	// Avoid automatic retries with another client authentication style. OIDC's
	// discovery default is client_secret_basic when no list is advertised.
	endpoint.AuthStyle = oauth2.AuthStyleInHeader
	if len(metadata.AuthMethods) > 0 {
		basic, post := false, false
		for _, m := range metadata.AuthMethods {
			basic = basic || m == "client_secret_basic"
			post = post || m == "client_secret_post"
		}
		if !basic {
			if !post {
				return nil, errOIDCProvider
			}
			endpoint.AuthStyle = oauth2.AuthStyleInParams
		}
	}
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), a.client), metadata.JWK)
	a.provider = &oidcProvider{oauth: oauth2.Config{ClientID: o.client, ClientSecret: o.secret, RedirectURL: o.callback, Scopes: []string{oidc.ScopeOpenID}, Endpoint: endpoint}, verifier: oidc.NewVerifier(o.issuer, keys, &oidc.Config{ClientID: o.client, SupportedSigningAlgs: []string{oidc.RS256, oidc.PS256, oidc.ES256}})}
	return a.provider, nil
}

func newOIDCHTTPClient() *http.Client {
	tr := &http.Transport{DialContext: dialOIDCPublic, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 32 << 10, IdleConnTimeout: 30 * time.Second, MaxIdleConns: 16, MaxConnsPerHost: 16}
	return &http.Client{Transport: oidcBoundedTransport{tr}, Timeout: oidcNetworkTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Resolve and filter on each new connection; dial the checked IP rather than
// resolving it again. TLS still verifies the configured DNS name. No implicit
// environment proxy can bypass this policy. Private IdPs are not supported.
func dialOIDCPublic(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errOIDCProvider
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, errOIDCProvider
	}
	d := net.Dialer{Timeout: 4 * time.Second}
	for _, ip := range ips {
		if !oidcPublicIP(ip) {
			continue
		}
		if conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port)); err == nil {
			return conn, nil
		}
	}
	return nil, errOIDCProvider
}

var oidcNonPublic = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("2002::/16")}

var oidcGlobalIPv6 = netip.MustParsePrefix("2000::/3")

func oidcPublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || (ip.Is6() && !oidcGlobalIPv6.Contains(ip)) {
		return false
	}
	for _, p := range oidcNonPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

type oidcBoundedTransport struct{ transport *http.Transport }

func (t oidcBoundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if oidcHTTPS(r.URL.String()) == nil {
		return nil, errOIDCProvider
	}
	res, err := t.transport.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if res.ContentLength > oidcMaxResponse {
		_ = res.Body.Close()
		return nil, errOIDCProvider
	}
	res.Body = &oidcBoundedBody{res.Body, oidcMaxResponse}
	return res, nil
}

type oidcBoundedBody struct {
	io.ReadCloser
	remaining int
}

func (b *oidcBoundedBody) Read(p []byte) (int, error) {
	if len(p) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	if n > b.remaining {
		return 0, errOIDCProvider
	}
	b.remaining -= n
	return n, err
}
