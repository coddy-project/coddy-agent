package llm

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"

	xproxy "golang.org/x/net/proxy"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// HTTPClientForProviderProxy returns the client for the requests a provider
// row makes outside a completion - the model list, the account usage, a
// sign-in - over the shared transport its completions use, so the row's
// providers[].proxy applies to every request it makes. The setting is read
// by config.ParseProxySetting: empty or "inherit" follows the environment's
// proxy (HTTPS_PROXY, HTTP_PROXY, NO_PROXY), "none" connects directly, and
// an http, https, socks5 or socks5h URL goes through that proxy.
func HTTPClientForProviderProxy(setting string) (*http.Client, error) {
	rt, err := providerTransport(setting)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: rt}, nil
}

// HTTPClientForOptionalProxy returns the client for the proxy setting of a
// caller that is not a provider row (the dry-run's Telegram probe), read by
// config.ParseProxySetting like providers[].proxy: an empty value or
// "inherit" returns nil, nil, so the caller keeps its default client and
// with it the environment's proxy; "none" returns a client that connects
// directly; a URL returns a client through that proxy. It builds a transport
// of its own rather than sharing a provider's.
func HTTPClientForOptionalProxy(setting string) (*http.Client, error) {
	mode, u, err := config.ParseProxySetting(setting)
	if err != nil {
		return nil, err
	}
	if mode == config.ProxyModeInherit {
		return nil, nil
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default transport is not *http.Transport")
	}
	t := base.Clone()
	if mode == config.ProxyModeNone {
		t.Proxy = nil
	} else if err := routeThroughProxy(t, u); err != nil {
		return nil, err
	}
	return &http.Client{Transport: t}, nil
}

// routeThroughProxy points t at the proxy u names: an HTTP or HTTPS proxy
// through Transport.Proxy, a SOCKS5 one through the dialer. A proxy URL is
// the row's whole route, so NO_PROXY and the loopback exception of the
// environment's proxy do not apply to it. x/net/proxy hands the target's
// host name to a SOCKS5 proxy as it is, so the proxy resolves it for socks5
// and socks5h alike.
func routeThroughProxy(t *http.Transport, u *url.URL) error {
	switch u.Scheme {
	case "http", "https":
		t.Proxy = http.ProxyURL(u)
		return nil
	case "socks5", "socks5h":
		dialer, err := xproxy.FromURL(u, xproxy.Direct)
		if err != nil {
			return fmt.Errorf("socks proxy: %w", err)
		}
		t.Proxy = nil
		if xd, ok := dialer.(xproxy.ContextDialer); ok {
			t.DialContext = xd.DialContext
		} else {
			t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return dialer.Dial(network, address)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported proxy scheme %q (use http, https, socks5, or socks5h)", u.Scheme)
	}
}
