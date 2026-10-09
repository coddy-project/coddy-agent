package config

import (
	"fmt"
	"regexp"
	"strings"
)

// ScopeSharedModels is the only scope of a relay client in this version: the
// three shared-model routes of the nodes behind the relay.
const ScopeSharedModels = "shared_models"

// SwarmClient is a relay client with a credential of its own that opens only
// the shared-model routes of the nodes it lists. swarm.auth_token stays the
// full class and is not an entry of this list.
type SwarmClient struct {
	// Name labels the entry in logs and counters. Unique, lower case.
	Name string `yaml:"name"`
	// Token is the bearer credential of the entry. An entry with no token (an
	// ${ENV} reference to an unset variable) can be authenticated by nothing and
	// is ignored.
	Token string `yaml:"token"`
	// Scope is the only thing the credential opens; shared_models.
	Scope string `yaml:"scope"`
	// Nodes are the hop paths the client may reach: "node" for a node directly
	// below this relay, "child/node" through a chained relay, "*" for any node
	// directly below this relay.
	Nodes []string `yaml:"nodes"`
	// MaxStreams caps the client's concurrent shared-model calls on this relay.
	MaxStreams int `yaml:"max_streams"`
	// RatePerMinute and RateBurst bound the calls the relay forwards for the
	// client in a window.
	RatePerMinute int `yaml:"rate_per_minute"`
	RateBurst     int `yaml:"rate_burst"`
}

const (
	swarmClientNamePattern = `^[a-z0-9][a-z0-9_-]{0,31}$`
	swarmNodeNamePattern   = `^[A-Za-z0-9_-]{1,64}$`
)

var (
	swarmClientNameRE = regexp.MustCompile(swarmClientNamePattern)
	swarmNodeNameRE   = regexp.MustCompile(swarmNodeNamePattern)
)

// EffectiveBurst resolves rate_burst: the written value, else the rate capped
// by max_streams, at least 1; 0 when the entry has no rate.
func (c SwarmClient) EffectiveBurst() int {
	if c.RatePerMinute <= 0 {
		return 0
	}
	if c.RateBurst > 0 {
		return c.RateBurst
	}
	burst := c.RatePerMinute
	if c.MaxStreams > 0 && c.MaxStreams < burst {
		burst = c.MaxStreams
	}
	if burst < 1 {
		burst = 1
	}
	return burst
}

// HasCredential reports whether anything can authenticate the entry.
func (c SwarmClient) HasCredential() bool {
	return strings.TrimSpace(c.Token) != ""
}

func (c *SwarmClient) normalize() {
	c.Name = strings.TrimSpace(c.Name)
	c.Token = strings.TrimSpace(c.Token)
	c.Scope = strings.TrimSpace(c.Scope)
	c.Nodes = trimmedStrings(c.Nodes)
}

// trimmedStrings trims every entry, keeping empty ones out.
func trimmedStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// validateClients reports an entry a relay could not act on. Every message
// starts with the config path of the key it is about, so a check places it in
// the file.
func (s *SwarmConfig) validateClients() error {
	names := map[string]bool{}
	for i, c := range s.Clients {
		at := func(key string) string { return fmt.Sprintf("swarm.clients[%d].%s", i, key) }
		switch {
		case c.Name == "":
			return fmt.Errorf("%s: every client needs a name", at("name"))
		case c.Name == "full" || c.Name == "unknown":
			return fmt.Errorf("%s: %q is reserved for the counters of the full class and of refused tokens", at("name"), c.Name)
		case !swarmClientNameRE.MatchString(c.Name):
			return fmt.Errorf("%s: %q is not a valid name: lower case letters, digits, '_' and '-', up to 32 characters", at("name"), c.Name)
		case names[c.Name]:
			return fmt.Errorf("%s: duplicate client name %q", at("name"), c.Name)
		}
		names[c.Name] = true
		switch c.Scope {
		case "":
			return fmt.Errorf("%s: scope is required (%s)", at("scope"), ScopeSharedModels)
		case ScopeSharedModels:
		default:
			return fmt.Errorf("%s: unknown scope %q, the only scope is %s", at("scope"), c.Scope, ScopeSharedModels)
		}
		if len(c.Nodes) == 0 {
			return fmt.Errorf("%s: list the nodes this client may reach, or \"*\" for every node directly below this relay", at("nodes"))
		}
		for _, n := range c.Nodes {
			if err := validateClientNode(n); err != nil {
				return fmt.Errorf("%s: %w", at("nodes"), err)
			}
		}
		if c.MaxStreams < 0 {
			return fmt.Errorf("%s: must not be negative", at("max_streams"))
		}
		if c.RatePerMinute < 0 {
			return fmt.Errorf("%s: must not be negative", at("rate_per_minute"))
		}
		if c.RateBurst < 0 {
			return fmt.Errorf("%s: must not be negative", at("rate_burst"))
		}
	}
	return nil
}

func validateClientNode(entry string) error {
	if entry == "*" {
		return nil
	}
	segs := strings.Split(entry, "/")
	if len(segs) > SwarmMaxHops {
		return fmt.Errorf("%q has %d hops, at most %d", entry, len(segs), SwarmMaxHops)
	}
	for _, seg := range segs {
		if !swarmNodeNameRE.MatchString(seg) {
			return fmt.Errorf("%q is not a node path: segments of letters, digits, '_' and '-' joined by '/' ('*' only as a whole entry)", entry)
		}
	}
	return nil
}

// ValidSharedAlias reports whether s is an alias a shared model can be offered
// under, for a component that needs the alphabet and not the whole config.
func ValidSharedAlias(s string) bool { return sharedAliasRE.MatchString(s) }

// The reserved label a node's registration carries when the token it joins with
// opens only the shared-model routes. internal/swarm owns the derivation and
// the wire; this package cannot import it, so it keeps its own copy of the key
// for the config check, and a test in internal/swarm holds the two equal.
const (
	LabelTokenClass        = "coddy.token_class"
	TokenClassSharedModels = "shared_models"
)
