package config

import (
	"crypto/subtle"
	"fmt"
	"strings"
)

// Defaults for the swarm relay.
const (
	// SwarmDefaultPort sits next to the HTTP gateway's 12345 so a host running
	// both needs no port arithmetic.
	SwarmDefaultPort = 12346
	// SwarmDefaultListenHost matches the HTTP gateway's default.
	SwarmDefaultListenHost = "0.0.0.0"
	// SwarmDefaultLeaseTTLSeconds is how long a registration stays valid
	// without a heartbeat. Nodes refresh at a third of it.
	SwarmDefaultLeaseTTLSeconds = 90
	// SwarmDefaultFanoutTimeoutSeconds bounds one node's contribution to an
	// aggregated answer. A slow node degrades into a warning, not a stall.
	SwarmDefaultFanoutTimeoutSeconds = 3
	// SwarmMaxHops caps how deep a request may travel through chained relays.
	SwarmMaxHops = 4
)

// SwarmConfig is the YAML swarm section (key swarm). It describes both sides of
// a relay: what this process serves under `swarm.enable`, and which parent
// relays this process joins, whether it is an agent or a relay itself.
type SwarmConfig struct {
	// Enabled runs the relay in this process. It is independent of Join: an
	// agent registering into a parent relay does not itself relay, and a relay
	// that chains into another one does both.
	Enabled bool `yaml:"enable"`
	// Host is the bind address for the relay when the CLI does not override it.
	Host string `yaml:"host"`
	// Port is the relay's listen port. Zero falls back to 12346.
	Port int `yaml:"port"`
	// Name labels this relay in topology views and in a child's node path.
	Name string `yaml:"name"`

	// AuthToken is the bearer credential clients present to this relay. A relay
	// proxies to every node it knows with that node's own credential, so an
	// unauthenticated relay bound off loopback is a fleet-wide open door: the
	// server refuses to start in that case unless AllowInsecure is set.
	AuthToken string `yaml:"auth_token"`
	// PairingTokens are the credentials a node must present to register. An
	// empty list means registration is closed unless InsecureOpenRegistration.
	PairingTokens []string `yaml:"pairing_tokens"`

	// AllowInsecure permits binding off loopback without a client token.
	AllowInsecure bool `yaml:"allow_insecure"`
	// InsecureOpenRegistration lets any caller register a node. Development only.
	InsecureOpenRegistration bool `yaml:"insecure_open_registration"`
	// AllowPrivateUpstreams lists hosts a node may advertise even though they
	// resolve into loopback or private ranges, which are otherwise refused so a
	// registration cannot turn the relay into a probe of its own network.
	AllowPrivateUpstreams []string `yaml:"allow_private_upstreams"`

	// CORS controls cross-origin access. The SPA talks to a relay from another
	// origin by construction, so this matters more here than on the agent.
	CORS HTTPCORSConfig `yaml:"cors"`

	// TLS serves the relay over HTTPS. Relays usually sit in different networks
	// and carry every node's credential, so the link between them is the last
	// place to leave in the clear.
	TLS SwarmTLSConfig `yaml:"tls"`

	// LeaseTTLSeconds overrides how long a registration survives without a
	// heartbeat.
	LeaseTTLSeconds int `yaml:"lease_ttl_seconds"`
	// FanoutTimeoutSeconds overrides the per-node deadline of an aggregated call.
	FanoutTimeoutSeconds int `yaml:"fanout_timeout_seconds"`

	// Upstreams are nodes configured by hand rather than registered. Useful for
	// an agent that cannot run the join loop.
	Upstreams []SwarmUpstream `yaml:"upstreams"`

	// Join lists parent relays this process registers into on startup. Every
	// `coddy serve` process honours it, relay or not, which is what lets relays
	// chain.
	Join []SwarmJoin `yaml:"join"`
}

// SwarmTLSConfig serves the relay over HTTPS. Both files or neither.
type SwarmTLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// Enabled reports whether TLS is configured.
func (t SwarmTLSConfig) Enabled() bool {
	return strings.TrimSpace(t.CertFile) != "" && strings.TrimSpace(t.KeyFile) != ""
}

// SwarmDialConfig is how one leg of the swarm reaches the other side. Relays
// are expected to live in different networks, so a proxy hop and a private
// certificate authority are ordinary, not exotic.
type SwarmDialConfig struct {
	// Proxy routes the connection through http, https, socks5 or socks5h.
	// Empty falls back to the standard environment variables.
	Proxy string `yaml:"proxy"`
	// CAFile verifies a peer whose certificate is signed privately.
	CAFile string `yaml:"ca_file"`
	// InsecureSkipVerify accepts any certificate. It exists for a lab and is
	// logged loudly every time it is used.
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
}

// SwarmUpstream is a node the relay knows about without being told by the node.
type SwarmUpstream struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
	// Kind is agent (default) or relay.
	Kind string `yaml:"kind"`
	// Token is the bearer credential the relay presents to this node.
	Token string `yaml:"token"`
	// Dial carries the proxy and TLS settings for reaching this node.
	Dial SwarmDialConfig `yaml:"dial"`
}

// SwarmJoin describes one parent relay this process registers into.
type SwarmJoin struct {
	// URL is the parent relay's origin.
	URL string `yaml:"url"`
	// Name is the name to claim in that relay. Empty falls back to the host name.
	Name string `yaml:"name"`
	// PairingToken authorises the registration.
	PairingToken string `yaml:"pairing_token"`
	// AdvertiseURL is where the relay can reach this process. Leaving it empty
	// selects the tunnel transport: this process dials out and serves its API
	// back over that connection, which is the only way in when the network
	// accepts no inbound connections.
	AdvertiseURL string `yaml:"advertise_url"`
	// Token is this process's own bearer credential, handed to the relay so it
	// can authenticate when it proxies. Prefer a credential minted for the
	// relay alone over the operator's own token.
	Token string `yaml:"token"`
	// Labels are free-form tags shown in topology views.
	Labels map[string]string `yaml:"labels"`
	// Dial carries the proxy and TLS settings for reaching the parent relay,
	// which matters most for the tunnel transport: that connection is the only
	// way in, so it is the one most likely to cross a proxy.
	Dial SwarmDialConfig `yaml:"dial"`
}

// EffectiveLeaseTTLSeconds resolves the configured lease lifetime.
func (s *SwarmConfig) EffectiveLeaseTTLSeconds() int {
	if s == nil || s.LeaseTTLSeconds <= 0 {
		return SwarmDefaultLeaseTTLSeconds
	}
	return s.LeaseTTLSeconds
}

// EffectiveFanoutTimeoutSeconds resolves the per-node aggregation deadline.
func (s *SwarmConfig) EffectiveFanoutTimeoutSeconds() int {
	if s == nil || s.FanoutTimeoutSeconds <= 0 {
		return SwarmDefaultFanoutTimeoutSeconds
	}
	return s.FanoutTimeoutSeconds
}

// EffectivePort resolves the listen port.
func (s *SwarmConfig) EffectivePort() int {
	if s == nil || s.Port == 0 {
		return SwarmDefaultPort
	}
	return s.Port
}

// EffectiveHost resolves the bind address.
func (s *SwarmConfig) EffectiveHost() string {
	if s == nil || strings.TrimSpace(s.Host) == "" {
		return SwarmDefaultListenHost
	}
	return strings.TrimSpace(s.Host)
}

// RegistrationOpen reports whether a node may register at all.
func (s *SwarmConfig) RegistrationOpen() bool {
	if s == nil {
		return false
	}
	return s.InsecureOpenRegistration || len(s.PairingTokens) > 0
}

// AcceptsPairingToken reports whether token authorises a registration.
func (s *SwarmConfig) AcceptsPairingToken(token string) bool {
	if s == nil {
		return false
	}
	if s.InsecureOpenRegistration {
		return true
	}
	for _, t := range s.PairingTokens {
		if t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(token)) == 1 {
			return true
		}
	}
	return false
}

// Normalize trims the string fields that later comparisons depend on.
func (s *SwarmConfig) Normalize() {
	if s == nil {
		return
	}
	s.Host = strings.TrimSpace(s.Host)
	s.Name = strings.TrimSpace(s.Name)
	s.AuthToken = strings.TrimSpace(s.AuthToken)
	for i := range s.PairingTokens {
		s.PairingTokens[i] = strings.TrimSpace(s.PairingTokens[i])
	}
	for i := range s.Upstreams {
		s.Upstreams[i].Name = strings.TrimSpace(s.Upstreams[i].Name)
		s.Upstreams[i].URL = strings.TrimRight(strings.TrimSpace(s.Upstreams[i].URL), "/")
		s.Upstreams[i].Kind = strings.TrimSpace(s.Upstreams[i].Kind)
		s.Upstreams[i].Token = strings.TrimSpace(s.Upstreams[i].Token)
	}
	for i := range s.Join {
		s.Join[i].URL = strings.TrimRight(strings.TrimSpace(s.Join[i].URL), "/")
		s.Join[i].Name = strings.TrimSpace(s.Join[i].Name)
		s.Join[i].PairingToken = strings.TrimSpace(s.Join[i].PairingToken)
		s.Join[i].AdvertiseURL = strings.TrimRight(strings.TrimSpace(s.Join[i].AdvertiseURL), "/")
		s.Join[i].Token = strings.TrimSpace(s.Join[i].Token)
	}
}

// Validate reports configuration a relay could not act on.
func (s *SwarmConfig) Validate() error {
	if s == nil {
		return nil
	}
	if s.Port < 0 || s.Port > 65535 {
		return fmt.Errorf("swarm.port %d is out of range", s.Port)
	}
	if s.LeaseTTLSeconds < 0 {
		return fmt.Errorf("swarm.lease_ttl_seconds must not be negative")
	}
	if s.FanoutTimeoutSeconds < 0 {
		return fmt.Errorf("swarm.fanout_timeout_seconds must not be negative")
	}
	certSet := strings.TrimSpace(s.TLS.CertFile) != ""
	keySet := strings.TrimSpace(s.TLS.KeyFile) != ""
	if certSet != keySet {
		return fmt.Errorf("swarm.tls: cert_file and key_file must be set together")
	}
	seen := map[string]bool{}
	for _, up := range s.Upstreams {
		if up.Name == "" {
			return fmt.Errorf("swarm.upstreams: every entry needs a name")
		}
		if seen[up.Name] {
			return fmt.Errorf("swarm.upstreams: duplicate node name %q", up.Name)
		}
		seen[up.Name] = true
		if up.URL == "" {
			return fmt.Errorf("swarm.upstreams %q: url is required", up.Name)
		}
		switch up.Kind {
		case "", "agent", "relay":
		default:
			return fmt.Errorf("swarm.upstreams %q: kind must be agent or relay", up.Name)
		}
	}
	for i, j := range s.Join {
		if j.URL == "" {
			return fmt.Errorf("swarm.join[%d]: url is required", i)
		}
	}
	return nil
}
