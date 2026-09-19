//go:build gateway || gateway.telegram

// Package proxyutil builds HTTP clients with optional proxy support for gateway adapters.
// The proxy setting reads like providers[].proxy (config.ParseProxySetting):
// inherit, none, or an http, https, socks5 or socks5h URL.
package proxyutil

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"golang.org/x/net/proxy"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// BuildHTTPClient returns the *http.Client the gateway reaches its API with.
// An empty setting or "inherit" returns http.DefaultClient unchanged, which
// follows the environment's proxy (HTTPS_PROXY, HTTP_PROXY, NO_PROXY); "none"
// returns a client that connects directly; a URL routes every request
// through that proxy.
func BuildHTTPClient(setting string) (*http.Client, error) {
	mode, u, err := config.ParseProxySetting(setting)
	if err != nil {
		return nil, err
	}
	switch mode {
	case config.ProxyModeInherit:
		return http.DefaultClient, nil
	case config.ProxyModeNone:
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, fmt.Errorf("default transport is not *http.Transport")
		}
		t := base.Clone()
		// No Proxy function at all: nothing in the environment is read.
		t.Proxy = nil
		return &http.Client{Transport: t}, nil
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return &http.Client{
			Transport: &http.Transport{Proxy: http.ProxyURL(u)},
		}, nil
	case "socks5", "socks5h":
		dialer, err := proxy.FromURL(u, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("socks5 proxy: %w", err)
		}
		return &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return dialer.Dial(network, addr)
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q (use http, https, socks5, or socks5h)", u.Scheme)
	}
}
