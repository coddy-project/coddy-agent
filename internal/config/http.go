package config

import (
	"fmt"
	"strconv"
	"strings"
)

// HTTPServerConfig controls the optional OpenAI-compatible HTTP gateway (built with -tags http). The embedded SPA requires -tags http,ui.
type HTTPServerConfig struct {
	// Enabled runs the HTTP API (and the embedded SPA) in this process. A nil
	// pointer means the default, true: the API is the surface `coddy serve`
	// exists for. Set `httpserver.enable: false` on a node that should only
	// poll a messenger or relay a swarm.
	Enabled *bool `yaml:"enable"`
	// Host is the default bind address when `coddy serve` does not override -H/--host. Empty falls back to 127.0.0.1, so a process that was started for some other subsystem never opens the API to the network by accident.
	Host string `yaml:"host"`
	// Port is the default listen port when `coddy serve` does not override -P/--port. Zero falls back to 12345.
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
	// CORS controls cross-origin access so a browser UI on another origin can call this API.
	CORS HTTPCORSConfig `yaml:"cors"`
	// Remotes lists remote coddy serve servers the bundled UI may connect to (environment
	// selector). Tokens are NOT stored here; the UI keeps them client-side per remote.
	Remotes []HTTPRemote `yaml:"remotes"`
}

// HTTPCORSConfig is the optional cross-origin policy for the HTTP gateway.
type HTTPCORSConfig struct {
	// Enabled turns on CORS handling (preflight + Access-Control-* headers).
	Enabled bool `yaml:"enable"`
	// AllowedOrigins are exact origins permitted to call the API (e.g. "http://localhost:5173").
	// A single "*" allows any origin (bearer auth still applies).
	AllowedOrigins []string `yaml:"allowed_origins"`
}

// HTTPRemote is one remote coddy serve server offered in the UI environment selector.
type HTTPRemote struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// CORSAllowOrigin returns the Access-Control-Allow-Origin value for origin and whether it is
// allowed. It returns "*" only when configured; otherwise it echoes the matched origin.
func (h *HTTPServerConfig) CORSAllowOrigin(origin string) (string, bool) {
	return h.CORS.AllowOrigin(origin)
}

// AllowOrigin answers the same question for any surface holding this policy,
// which the swarm relay needs because it carries its own CORS settings.
func (c HTTPCORSConfig) AllowOrigin(origin string) (string, bool) {
	if !c.Enabled || strings.TrimSpace(origin) == "" {
		return "", false
	}
	for _, o := range c.AllowedOrigins {
		o = strings.TrimSpace(o)
		if o == "*" {
			return "*", true
		}
		if strings.EqualFold(o, origin) {
			return origin, true
		}
	}
	return "", false
}

// EffectiveAuthTokens returns the configured token as a slice (empty when unset), so callers can
// union it with out-of-band tokens (--auth-token / CODDY_HTTP_TOKEN) uniformly.
func (h *HTTPServerConfig) EffectiveAuthTokens() []string {
	if s := strings.TrimSpace(h.AuthToken); s != "" {
		return []string{s}
	}
	return nil
}

// Normalize trims host, the auth token, CORS origins, and remote entries.
func (h *HTTPServerConfig) Normalize() {
	h.Host = strings.TrimSpace(h.Host)
	h.AuthToken = strings.TrimSpace(h.AuthToken)
	for i := range h.CORS.AllowedOrigins {
		h.CORS.AllowedOrigins[i] = strings.TrimSpace(h.CORS.AllowedOrigins[i])
	}
	for i := range h.Remotes {
		h.Remotes[i].Name = strings.TrimSpace(h.Remotes[i].Name)
		h.Remotes[i].URL = strings.TrimSpace(h.Remotes[i].URL)
	}
}

// Validate checks HTTP settings when present in config.
func (h *HTTPServerConfig) Validate() error {
	if h.Port < 0 || h.Port > 65535 {
		return fmt.Errorf("httpserver.port out of range")
	}
	return nil
}

// IsEnabled reports whether this process should serve the HTTP API. Unset means true.
func (h *HTTPServerConfig) IsEnabled() bool {
	return h == nil || h.Enabled == nil || *h.Enabled
}

// DefaultListenHost returns YAML host or the loopback fallback when omitted.
//
// The fallback is deliberately not 0.0.0.0: one process now starts every
// subsystem the config enables, so an operator who asked only for a Telegram
// bot must not find the agent API listening on every interface as a side
// effect. Reaching it from another machine is an explicit `httpserver.host`
// or -H away.
func (h *HTTPServerConfig) DefaultListenHost() string {
	if s := strings.TrimSpace(h.Host); s != "" {
		return s
	}
	return "127.0.0.1"
}

// DefaultListenPortString returns YAML port or the CLI fallback when zero.
func (h *HTTPServerConfig) DefaultListenPortString() string {
	if h.Port > 0 {
		return strconv.Itoa(h.Port)
	}
	return "12345"
}
