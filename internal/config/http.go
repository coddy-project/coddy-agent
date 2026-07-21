package config

import (
	"fmt"
	"strconv"
	"strings"
)

// HTTPServerConfig controls the optional OpenAI-compatible HTTP gateway (built with -tags http). The embedded SPA requires -tags http,ui.
type HTTPServerConfig struct {
	// Host is the default bind address when coddy http does not override -H/--host (e.g. "127.0.0.1"). Empty falls back to 0.0.0.0 in the CLI.
	Host string `yaml:"host"`
	// Port is the default listen port when coddy http does not override -P/--port. Zero falls back to 12345 in the CLI.
	Port int `yaml:"port"`
	// AuthToken is the optional bearer credential for the HTTP API. Empty means no authentication
	// (historical "no login" behavior). "${ENV}" references are expanded at load. The HTTP layer
	// never echoes it back through GET /coddy/config. Prefer --auth-token / CODDY_HTTP_TOKEN to
	// keep the secret out of config.yaml. See docs/remote-control.md.
	AuthToken string `yaml:"auth_token"`
	// PublicDocs keeps /docs and /openapi.* reachable without a token even when auth is enabled.
	PublicDocs bool `yaml:"public_docs"`
	// AllowInsecure silences the startup warning about a non-loopback bind without authentication.
	AllowInsecure bool `yaml:"allow_insecure"`
}

// EffectiveAuthTokens returns the configured token as a slice (empty when unset), so callers can
// union it with out-of-band tokens (--auth-token / CODDY_HTTP_TOKEN) uniformly.
func (h *HTTPServerConfig) EffectiveAuthTokens() []string {
	if s := strings.TrimSpace(h.AuthToken); s != "" {
		return []string{s}
	}
	return nil
}

// Normalize trims host and the auth token.
func (h *HTTPServerConfig) Normalize() {
	h.Host = strings.TrimSpace(h.Host)
	h.AuthToken = strings.TrimSpace(h.AuthToken)
}

// Validate checks HTTP settings when present in config.
func (h *HTTPServerConfig) Validate() error {
	if h.Port < 0 || h.Port > 65535 {
		return fmt.Errorf("httpserver.port out of range")
	}
	return nil
}

// DefaultListenHost returns YAML host or the CLI fallback when omitted.
func (h *HTTPServerConfig) DefaultListenHost() string {
	if s := strings.TrimSpace(h.Host); s != "" {
		return s
	}
	return "0.0.0.0"
}

// DefaultListenPortString returns YAML port or the CLI fallback when zero.
func (h *HTTPServerConfig) DefaultListenPortString() string {
	if h.Port > 0 {
		return strconv.Itoa(h.Port)
	}
	return "12345"
}
