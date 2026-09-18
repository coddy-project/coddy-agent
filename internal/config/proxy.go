package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

var allowedProviderProxySchemes = map[string]struct{}{
	"http":    {},
	"https":   {},
	"socks5":  {},
	"socks5h": {},
}

// The words providers[].proxy accepts besides a proxy URL.
const (
	// ProviderProxyInherit routes a provider's requests the way the
	// environment of the Coddy process says: HTTPS_PROXY, HTTP_PROXY and
	// NO_PROXY as net/http reads them. It is the default; an empty value
	// means the same.
	ProviderProxyInherit = "inherit"
	// ProviderProxyNone connects a provider directly: the environment's
	// proxy variables are ignored for its requests.
	ProviderProxyNone = "none"
)

// ProviderProxyMode is the route a providers[].proxy value selects.
type ProviderProxyMode int

const (
	// ProviderProxyModeInherit follows the environment's proxy (empty or
	// "inherit").
	ProviderProxyModeInherit ProviderProxyMode = iota
	// ProviderProxyModeNone connects directly ("none").
	ProviderProxyModeNone
	// ProviderProxyModeURL goes through the proxy the value names.
	ProviderProxyModeURL
)

// ParseProviderProxy reads a providers[].proxy value. Empty and "inherit"
// follow the environment's proxy, "none" connects directly (both words in
// any case), and anything else must be an http, https, socks5 or socks5h URL
// with a host, returned parsed. An error never repeats the value: a proxy
// URL may carry a password.
func ParseProviderProxy(s string) (ProviderProxyMode, *url.URL, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "" || strings.EqualFold(s, ProviderProxyInherit):
		return ProviderProxyModeInherit, nil, nil
	case strings.EqualFold(s, ProviderProxyNone):
		return ProviderProxyModeNone, nil, nil
	}
	u, err := url.Parse(s)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, nil, fmt.Errorf("proxy: not a valid URL (%v)", err)
	}
	if u.Scheme == "" {
		if !strings.Contains(s, "/") && !strings.Contains(s, ":") {
			// A bare word, such as the "direct" of an http_request call.
			return 0, nil, fmt.Errorf("proxy: unknown value; use %q to connect directly, %q (or no value) to follow the environment's proxy, or a proxy URL (http, https, socks5, socks5h)",
				ProviderProxyNone, ProviderProxyInherit)
		}
		return 0, nil, fmt.Errorf("proxy: scheme is required (http, https, socks5, or socks5h)")
	}
	if _, ok := allowedProviderProxySchemes[u.Scheme]; !ok {
		return 0, nil, fmt.Errorf("proxy: unsupported scheme %q (use http, https, socks5, or socks5h)", u.Scheme)
	}
	if strings.TrimSpace(u.Host) == "" {
		return 0, nil, fmt.Errorf("proxy: host is required")
	}
	return ProviderProxyModeURL, u, nil
}

// normalizeProviderProxy trims a providers[].proxy value and spells its
// keywords in lower case. A URL keeps its case: a password is case
// sensitive.
func normalizeProviderProxy(s string) string {
	s = strings.TrimSpace(s)
	for _, kw := range []string{ProviderProxyInherit, ProviderProxyNone} {
		if strings.EqualFold(s, kw) {
			return kw
		}
	}
	return s
}

// validateProviderProxy checks an optional providers[].proxy value.
func validateProviderProxy(s string) error {
	_, _, err := ParseProviderProxy(s)
	return err
}
