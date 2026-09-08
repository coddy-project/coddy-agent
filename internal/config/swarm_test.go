package config

import (
	"strings"
	"testing"
)

// Every swarm credential is write-only, so a client that reads the config and
// writes it back sends them empty. Without preservation a single save from the
// settings screen would leave a relay that refuses its own fleet.
func TestSwarmSecretsSurviveASave(t *testing.T) {
	current := &Config{}
	current.Swarm.AuthToken = "client-secret"
	current.Swarm.PairingTokens = []string{"pair-secret"}
	current.Swarm.Upstreams = []SwarmUpstream{
		{Name: "nas02", URL: "https://nas02:12345", Token: "nas02-token",
			Dial: SwarmDialConfig{Proxy: "socks5://proxy:1080"}},
	}
	current.Swarm.Join = []SwarmJoin{
		{URL: "https://relay.example", Name: "me", PairingToken: "join-pair", Token: "my-token"},
	}

	// What a client sends back after reading: the shape, none of the secrets.
	next := &Config{}
	next.Swarm.Upstreams = []SwarmUpstream{{Name: "nas02", URL: "https://nas02:12345"}}
	next.Swarm.Join = []SwarmJoin{{URL: "https://relay.example", Name: "me"}}
	preserveSwarmSecrets(&next.Swarm, &current.Swarm)

	if next.Swarm.AuthToken != "client-secret" {
		t.Error("the relay's own client token was lost")
	}
	if len(next.Swarm.PairingTokens) != 1 {
		t.Error("the pairing tokens were lost")
	}
	if next.Swarm.Upstreams[0].Token != "nas02-token" {
		t.Error("a node credential was lost")
	}
	if next.Swarm.Upstreams[0].Dial.Proxy != "socks5://proxy:1080" {
		t.Error("a proxy url was lost")
	}
	if next.Swarm.Join[0].PairingToken != "join-pair" || next.Swarm.Join[0].Token != "my-token" {
		t.Error("join credentials were lost")
	}
}

// A credential belongs to a destination, not to a label: pointing an entry
// somewhere new must not hand that node's token to the new address.
func TestSwarmSecretsDoNotFollowARedirectedNode(t *testing.T) {
	current := &Config{}
	current.Swarm.Upstreams = []SwarmUpstream{
		{Name: "nas02", URL: "https://nas02:12345", Token: "nas02-token"},
	}
	current.Swarm.Join = []SwarmJoin{
		{URL: "https://relay.example", Name: "me", Token: "my-token"},
	}

	next := &Config{}
	next.Swarm.Upstreams = []SwarmUpstream{{Name: "nas02", URL: "https://attacker.example"}}
	next.Swarm.Join = []SwarmJoin{{URL: "https://attacker.example", Name: "me"}}
	preserveSwarmSecrets(&next.Swarm, &current.Swarm)

	if next.Swarm.Upstreams[0].Token != "" {
		t.Fatalf("a node credential followed the entry to a new address: %q", next.Swarm.Upstreams[0].Token)
	}
	if next.Swarm.Join[0].Token != "" {
		t.Fatalf("a join credential followed the entry to a new relay: %q", next.Swarm.Join[0].Token)
	}
}

// A "$" in a proxy password would be read as an environment reference on the
// next load and expand to nothing.
func TestSwarmProxyDollarsSurviveARoundTrip(t *testing.T) {
	cfg := &Config{}
	cfg.Swarm.Join = []SwarmJoin{
		{URL: "https://relay.example", Dial: SwarmDialConfig{Proxy: "socks5://user:pa$$word@proxy:1080"}},
	}
	yb, err := MarshalConfigYAML(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(yb), "$$$$word") && !strings.Contains(string(yb), "$$") {
		t.Fatalf("the dollars were not escaped for disk: %s", yb)
	}
	// The live config keeps the real value: only the copy written out is escaped.
	if cfg.Swarm.Join[0].Dial.Proxy != "socks5://user:pa$$word@proxy:1080" {
		t.Fatalf("escaping mutated the live config: %q", cfg.Swarm.Join[0].Dial.Proxy)
	}
}
