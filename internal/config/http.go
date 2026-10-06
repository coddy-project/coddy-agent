package config

import (
	"fmt"
	"net"
	"net/url"
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
	// AllowInsecure silences the two startup warnings about a server without
	// authentication - a non-loopback bind, and CORS that admits pages nobody
	// listed (allow_loopback or "*") - and the --dry-run findings that mirror them.
	AllowInsecure bool `yaml:"allow_insecure"`
	// CORS controls cross-origin access so a browser UI on another origin can call this API.
	CORS HTTPCORSConfig `yaml:"cors"`
	// Remotes lists remote coddy serve servers and swarm relays the bundled UI may connect
	// to (environment selector) and `coddy --remote <name>` resolves. An entry may carry
	// the token to present to it; without one the UI keeps the token client-side per
	// remote and the console takes it from --remote-token or CODDY_REMOTE_TOKEN.
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

// HTTPCORSConfig is the optional cross-origin policy for the HTTP gateway and,
// as swarm.cors, for the relay. CORS decides whether a browser shows a page the
// answer; the token or the sign-in form decides whether the server gives one,
// so none of these settings is a credential.
type HTTPCORSConfig struct {
	// Enabled turns on CORS handling (preflight + Access-Control-* headers).
	Enabled bool `yaml:"enable"`
	// AllowLoopback also admits every page served from the browser's own
	// machine: an http or https origin whose host is localhost, a *.localhost
	// name, 127.0.0.0/8 or [::1], on any port (an IPv4-mapped IPv6 spelling
	// such as [::ffff:127.0.0.1] is none of those: list it in AllowedOrigins if
	// it is wanted). It is the laptop case of a remote coddy serve - the web UI
	// comes from the laptop's own coddy serve, and its port moves between
	// installations - where AllowedOrigins would need the exact string of each.
	// Narrower than "*", and like "*" only as safe as the credential behind the
	// API.
	AllowLoopback bool `yaml:"allow_loopback"`
	// AllowedOrigins are exact origins permitted to call the API (e.g. "http://localhost:5173").
	// A single "*" allows any origin (bearer auth still applies).
	AllowedOrigins []string `yaml:"allowed_origins"`
}

// HTTPRemote is one remote coddy serve server (or swarm relay) offered in the UI
// environment selector.
type HTTPRemote struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
	// Token is the bearer token to present to the remote - a relay's client token
	// (swarm.auth_token) or a server's httpserver.auth_token - usually written as a
	// ${ENV} reference. Optional: an entry that carries it hands it to every browser
	// that reads this configuration (GET /coddy/config, like a provider's api_key)
	// and to `coddy --remote <name>`, which is the choice of whoever wrote the entry.
	Token string `yaml:"token,omitempty"`
}

// CORSAllowOrigin returns the Access-Control-Allow-Origin value for origin and whether it is
// allowed. It returns "*" only when configured; otherwise it echoes the matched origin.
func (h *HTTPServerConfig) CORSAllowOrigin(origin string) (string, bool) {
	return h.CORS.AllowOrigin(origin)
}

// AllowOrigin answers the same question for any surface holding this policy,
// which the swarm relay needs because it carries its own CORS settings. The
// list is consulted first, so "*" keeps winning; a loopback origin admitted by
// AllowLoopback is echoed, never widened to "*".
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
	if c.AllowLoopback && isLoopbackOrigin(origin) {
		return origin, true
	}
	return "", false
}

// OpenToUnlistedOrigins reports whether the policy admits pages nobody named:
// "*" in the list, or AllowLoopback. Either is only as safe as the credential
// behind the API, which is what the startup warning and the dry run say when
// there is none.
func (c HTTPCORSConfig) OpenToUnlistedOrigins() bool {
	if !c.Enabled {
		return false
	}
	if c.AllowLoopback {
		return true
	}
	for _, o := range c.AllowedOrigins {
		if strings.TrimSpace(o) == "*" {
			return true
		}
	}
	return false
}

// isLoopbackOrigin reports whether origin is a serialized http or https origin
// - scheme, host, optional port and nothing else - whose host is loopback. The
// test is syntactic: no DNS, so a name that resolves to loopback on the
// browser's machine but is not spelled as loopback is refused, and so is a URL
// with a path, a query, a fragment or user information, which a browser never
// sends as Origin and which must not be echoed into Access-Control-Allow-Origin.
func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	// A serialized origin is the scheme, the host and an optional port and
	// nothing else. Rebuilding it from the parsed parts and comparing drops a
	// path, a query (an empty one leaves only ForceQuery behind), a fragment,
	// user information and a trailing slash in one check.
	if u.ForceQuery || !strings.EqualFold(u.Scheme+"://"+u.Host, origin) {
		return false
	}
	host := u.Host
	if h, port, err := net.SplitHostPort(host); err == nil {
		if p, err := strconv.ParseUint(port, 10, 16); err != nil || p == 0 {
			return false
		}
		host = h
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	} else if strings.Contains(host, ":") {
		// An IPv6 literal belongs in brackets; bare colons are neither a
		// port nor an origin a browser would send.
		return false
	}
	if host == "" {
		return false
	}
	return isLoopbackOriginHost(host)
}

// isLoopbackOriginHost is the host half of the origin test: localhost, a
// *.localhost name, an address of 127.0.0.0/8 in dotted form, or ::1 in any
// spelling of it. It is narrower than isLoopbackHostname on purpose. A bind
// address is judged by that one; a page's origin, which the echo makes a
// statement about, by this:
//   - net.IP.IsLoopback also admits an IPv4-mapped IPv6 address, and the page
//     at [::ffff:127.0.0.1] has the origin http://[::ffff:7f00:1]:<port>, which
//     is neither spelling the setting names;
//   - url.Parse tolerates characters in a host (a bracket, a non-ASCII letter)
//     that no browser sends in a name, so a name is checked for the characters
//     of a host name before its suffix is read.
func isLoopbackOriginHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		if strings.Contains(host, ":") {
			return ip.Equal(net.IPv6loopback)
		}
		return ip.IsLoopback()
	}
	if !isHostNameText(host) {
		return false
	}
	// RFC 6761 reserves *.localhost for loopback and browsers resolve it so
	// without a hosts entry, which is what makes it a usable alias scheme.
	host = strings.ToLower(host)
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

// isHostNameText reports whether s is made only of the characters a host name
// is written with: letters, digits, hyphen, underscore and dot.
func isHostNameText(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return s != ""
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
		h.Remotes[i].Token = strings.TrimSpace(h.Remotes[i].Token)
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
