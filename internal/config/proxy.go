package config

import (
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

// validateProviderProxyURL checks optional per-provider proxy URL (http, https, socks5, socks5h).
func validateProviderProxyURL(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("proxy: %w", err)
	}
	if u.Scheme == "" {
		return fmt.Errorf("proxy: scheme is required")
	}
	scheme := strings.ToLower(u.Scheme)
	if _, ok := allowedProviderProxySchemes[scheme]; !ok {
		return fmt.Errorf("proxy: unsupported scheme %q (use http, https, socks5, or socks5h)", u.Scheme)
	}
	if strings.TrimSpace(u.Host) == "" {
		return fmt.Errorf("proxy: host is required")
	}
	return nil
}
