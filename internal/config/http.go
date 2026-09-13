package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
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
	// keep the secret out of config.yaml. See docs/plans/remote-control.md.
	AuthToken string `yaml:"auth_token"`
	// Login is the optional sign-in form for the web UI: a browser presents a
	// user and a password once and carries a cookie afterwards, where an API
	// client presents AuthToken on every call. Both open the same gate.
	Login HTTPLoginConfig `yaml:"login"`
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

// LoginMode names how a browser proves who it is.
//
// Only the password form ships today; the key exists so the trusted-proxy mode
// (an identity header from an SSO gateway in front of Coddy) can be added later
// without moving anybody's configuration.
const (
	LoginModePassword = "password"
)

// HTTPLoginConfig is the optional web sign-in: one operator account, a
// server-side session and an HttpOnly cookie.
//
// It is off unless an account exists. Credentials may also come from the
// environment (CODDY_HTTP_USER / CODDY_HTTP_PASSWORD, typically through
// $CODDY_HOME/.env), which is why the switch is a pointer: nil means "on when
// an account is configured anywhere", and only an explicit false turns the form
// off with the variables still set.
type HTTPLoginConfig struct {
	// Enabled turns the sign-in form on or off explicitly. Nil follows the
	// credentials: a configured account enables the form, no account leaves the
	// server exactly as it was before this option existed.
	Enabled *bool `yaml:"enable"`
	// Mode is how a browser authenticates. Empty means LoginModePassword, the
	// only mode this version implements.
	Mode string `yaml:"mode"`
	// User is the account name. "${ENV}" references are expanded at load, so
	// user: "${CODDY_HTTP_USER}" works like every other value in this file.
	User string `yaml:"user"`
	// PasswordHash is an argon2id hash in PHC form, written by
	// `coddy serve set-password`. The HTTP layer never echoes it back through
	// GET /coddy/config, and a save from the settings screen preserves it.
	PasswordHash string `yaml:"password_hash"`
	// SessionTTLHours is how long a browser stays signed in. Zero means the
	// cookie is dropped when the browser closes; the server still bounds the
	// session it holds (webauth.DefaultSessionTTL), because it cannot see a
	// browser close and a record it keeps forever is not a session.
	SessionTTLHours int `yaml:"session_ttl_hours"`
}

// Normalize trims the login fields.
func (l *HTTPLoginConfig) Normalize() {
	l.Mode = strings.ToLower(strings.TrimSpace(l.Mode))
	l.User = strings.TrimSpace(l.User)
	l.PasswordHash = strings.TrimSpace(l.PasswordHash)
}

// Validate reports the login settings a server could not honour.
//
// Whether an account exists is deliberately not checked here: the credentials
// may come from the environment, which this package never reads into the
// document it would then write back to disk. The server resolves both sources
// and refuses to start when a form is asked for that nobody can pass.
func (l *HTTPLoginConfig) Validate() error {
	switch l.Mode {
	case "", LoginModePassword:
	default:
		return fmt.Errorf("httpserver.login.mode %q is not supported (only %q)", l.Mode, LoginModePassword)
	}
	if l.SessionTTLHours < 0 {
		return fmt.Errorf("httpserver.login.session_ttl_hours must not be negative")
	}
	if h := l.PasswordHash; h != "" && !webauth.IsHash(h) {
		// The most likely cause is a hash pasted in by hand: every "$" of it was
		// read as an environment reference when the file loaded. The command
		// below writes the same hash with the signs doubled, which is how a
		// literal "$" is spelled everywhere in this file.
		return fmt.Errorf("httpserver.login.password_hash is not an argon2id hash " +
			"(a hand-written one needs every \"$\" doubled, as \"$$argon2id$$v=19$$...\"); " +
			"write it with `coddy serve set-password`")
	}
	return nil
}

// HasAccount reports whether this file carries a complete account.
func (l *HTTPLoginConfig) HasAccount() bool {
	return l.User != "" && l.PasswordHash != ""
}

// IsExplicitlyDisabled reports an `enable: false` the operator wrote, which wins
// over credentials from anywhere, including the environment.
func (l *HTTPLoginConfig) IsExplicitlyDisabled() bool {
	return l.Enabled != nil && !*l.Enabled
}

// IsExplicitlyEnabled reports an `enable: true` the operator wrote. A server
// that finds no account behind it refuses to start rather than pretend.
func (l *HTTPLoginConfig) IsExplicitlyEnabled() bool {
	return l.Enabled != nil && *l.Enabled
}

// SessionTTL is how long a session lives, or zero for "until the browser
// closes", which is what the cookie then says too - the server still applies
// webauth.DefaultSessionTTL to its own record of it.
func (l *HTTPLoginConfig) SessionTTL() time.Duration {
	if l.SessionTTLHours <= 0 {
		return 0
	}
	return time.Duration(l.SessionTTLHours) * time.Hour
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

// Normalize trims host, the auth token, the login account, CORS origins, and remote entries.
func (h *HTTPServerConfig) Normalize() {
	h.Host = strings.TrimSpace(h.Host)
	h.AuthToken = strings.TrimSpace(h.AuthToken)
	h.Login.Normalize()
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
	return h.Login.Validate()
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
