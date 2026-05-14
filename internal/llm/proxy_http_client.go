package llm

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	xproxy "golang.org/x/net/proxy"
)

// HTTPClientForOptionalProxy returns an HTTP client that sends traffic through the given proxy URL.
// For an empty proxyURL it returns nil, nil so callers keep the SDK default client.
// Supported schemes are http, https (HTTP proxy), socks5, and socks5h (SOCKS5 with remote DNS on socks5h).
func HTTPClientForOptionalProxy(proxyURL string) (*http.Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return nil, nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("proxy url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https":
		t, err := transportHTTPProxy(u)
		if err != nil {
			return nil, err
		}
		return &http.Client{Transport: t}, nil
	case "socks5", "socks5h":
		t, err := transportSOCKSProxy(u)
		if err != nil {
			return nil, err
		}
		return &http.Client{Transport: t}, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q (use http, https, socks5, or socks5h)", u.Scheme)
	}
}

func transportHTTPProxy(u *url.URL) (*http.Transport, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default transport is not *http.Transport")
	}
	t := base.Clone()
	t.Proxy = http.ProxyURL(u)
	return t, nil
}

func transportSOCKSProxy(u *url.URL) (*http.Transport, error) {
	dialer, err := xproxy.FromURL(u, xproxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("socks proxy: %w", err)
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default transport is not *http.Transport")
	}
	t := base.Clone()
	t.Proxy = nil
	if xd, ok := dialer.(xproxy.ContextDialer); ok {
		t.DialContext = xd.DialContext
	} else {
		t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.Dial(network, address)
		}
	}
	return t, nil
}
