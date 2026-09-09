package config

import (
	"os"
	"path/filepath"
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

// A single "$" in a proxy password is what the load-time expansion pass would
// read as an environment reference and quietly turn into nothing. The test uses
// one, writes the file, and loads it back, so an escaping no-op fails here.
func TestSwarmProxyDollarsSurviveAWriteAndReload(t *testing.T) {
	const secret = "socks5://user:pa$word@proxy:1080"

	cfg := &Config{}
	cfg.Swarm.Join = []SwarmJoin{{URL: "https://relay.example", Dial: SwarmDialConfig{Proxy: secret}}}
	cfg.Swarm.Upstreams = []SwarmUpstream{
		{Name: "nas02", URL: "https://nas02:1", Dial: SwarmDialConfig{Proxy: secret}},
	}

	yb, err := MarshalConfigYAML(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The live config is untouched: only the copy written out is escaped.
	if cfg.Swarm.Join[0].Dial.Proxy != secret {
		t.Fatalf("escaping mutated the live config: %q", cfg.Swarm.Join[0].Dial.Proxy)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, yb, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFromCLI(CLIPaths{Config: path, Home: dir})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := loaded.Swarm.Join[0].Dial.Proxy; got != secret {
		t.Fatalf("a join proxy came back as %q, want %q", got, secret)
	}
	if got := loaded.Swarm.Upstreams[0].Dial.Proxy; got != secret {
		t.Fatalf("an upstream proxy came back as %q, want %q", got, secret)
	}
}

// Renaming an entry is a label change; the credential belongs to the address it
// points at and has to survive one.
func TestSwarmSecretsSurviveARename(t *testing.T) {
	current := &Config{}
	current.Swarm.Upstreams = []SwarmUpstream{
		{Name: "nas02", URL: "https://nas02:12345", Token: "nas02-token"},
	}
	current.Swarm.Join = []SwarmJoin{
		{URL: "https://relay.example", Name: "old-name", Token: "my-token"},
	}

	next := &Config{}
	next.Swarm.Upstreams = []SwarmUpstream{{Name: "storage-box", URL: "https://nas02:12345"}}
	next.Swarm.Join = []SwarmJoin{{URL: "https://relay.example", Name: "new-name"}}
	preserveSwarmSecrets(&next.Swarm, &current.Swarm)

	if next.Swarm.Upstreams[0].Token != "nas02-token" {
		t.Error("renaming an upstream lost its credential")
	}
	if next.Swarm.Join[0].Token != "my-token" {
		t.Error("renaming a join entry lost its credential")
	}
}

// Two entries pointing at one address cannot be told apart by address alone, so
// a rename there keeps nothing rather than guessing.
func TestSwarmSecretsDoNotGuessBetweenTwoEntriesAtOneAddress(t *testing.T) {
	current := &Config{}
	current.Swarm.Upstreams = []SwarmUpstream{
		{Name: "a", URL: "https://shared:1", Token: "token-a"},
		{Name: "b", URL: "https://shared:1", Token: "token-b"},
	}
	next := &Config{}
	next.Swarm.Upstreams = []SwarmUpstream{{Name: "c", URL: "https://shared:1"}}
	preserveSwarmSecrets(&next.Swarm, &current.Swarm)
	if next.Swarm.Upstreams[0].Token != "" {
		t.Fatalf("an ambiguous rename guessed a credential: %q", next.Swarm.Upstreams[0].Token)
	}
}
