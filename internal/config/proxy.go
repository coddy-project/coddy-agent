package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

var allowedProxySchemes = map[string]struct{}{
	"http":    {},
	"https":   {},
	"socks5":  {},
	"socks5h": {},
}

// The words a proxy setting (providers[].proxy, gateways.telegram.proxy)
// accepts besides a proxy URL.
const (
	// ProxyInherit routes the requests the way the environment of the Coddy
	// process says: HTTPS_PROXY, HTTP_PROXY and NO_PROXY as net/http reads
	// them. It is the default; an empty value means the same.
	ProxyInherit = "inherit"
	// ProxyNone connects directly: the environment's proxy variables are
	// ignored for these requests.
	ProxyNone = "none"
)

// ProxyMode is the route a proxy setting selects.
type ProxyMode int

const (
	// ProxyModeInherit follows the environment's proxy (empty or
	// "inherit").
	ProxyModeInherit ProxyMode = iota
	// ProxyModeNone connects directly ("none").
	ProxyModeNone
	// ProxyModeURL goes through the proxy the value names.
	ProxyModeURL
)

// ParseProxySetting reads a proxy setting (providers[].proxy,
// gateways.telegram.proxy). Empty and "inherit" follow the environment's
// proxy, "none" connects directly (both words in any case), and anything
// else must be an http, https, socks5 or socks5h URL with a host, returned
// parsed. An error never repeats the value: a proxy URL may carry a
// password.
func ParseProxySetting(s string) (ProxyMode, *url.URL, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "" || strings.EqualFold(s, ProxyInherit):
		return ProxyModeInherit, nil, nil
	case strings.EqualFold(s, ProxyNone):
		return ProxyModeNone, nil, nil
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
				ProxyNone, ProxyInherit)
		}
		return 0, nil, fmt.Errorf("proxy: scheme is required (http, https, socks5, or socks5h)")
	}
	if _, ok := allowedProxySchemes[u.Scheme]; !ok {
		return 0, nil, fmt.Errorf("proxy: unsupported scheme %q (use http, https, socks5, or socks5h)", u.Scheme)
	}
	if strings.TrimSpace(u.Host) == "" {
		return 0, nil, fmt.Errorf("proxy: host is required")
	}
	return ProxyModeURL, u, nil
}

// normalizeProxySetting trims a proxy setting and spells its
// keywords in lower case. A URL keeps its case: a password is case
// sensitive.
func normalizeProxySetting(s string) string {
	s = strings.TrimSpace(s)
	for _, kw := range []string{ProxyInherit, ProxyNone} {
		if strings.EqualFold(s, kw) {
			return kw
		}
	}
	return s
}

// validateProxySetting checks an optional proxy setting.
func validateProxySetting(s string) error {
	_, _, err := ParseProxySetting(s)
	return err
}
