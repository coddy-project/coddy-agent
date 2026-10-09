package swarm

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/pki"
)

// A node joining a relay that requires client certificates presents the pair of
// its dial settings: the tunnel and the heartbeat take these options as they are.
func TestStartJoinsCarriesTheDialCertificate(t *testing.T) {
	relay := newFakeRelay(t, "pair")
	cfg := &config.Config{}
	cfg.Swarm.Join = []config.SwarmJoin{{
		URL: relay.ts.URL, Name: "node-a", PairingToken: "pair", AdvertiseURL: "http://a:1",
		Dial: config.SwarmDialConfig{CertFile: "/etc/c.pem", KeyFile: "/etc/k.pem"},
	}}
	set, err := StartJoins(context.Background(), cfg, StartJoinsOptions{
		Kind: KindAgent, Handler: http.NotFoundHandler(), Log: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Stop()
	got := set.Clients()[0].opts.Dial
	if got.CertFile != "/etc/c.pem" || got.KeyFile != "/etc/k.pem" {
		t.Fatalf("dial options = %+v, want the certificate pair", got)
	}
}

// A join with dial.auto dials with the built-in bundle and client pair of the configuration's home, and a file named by hand wins.
func TestStartJoinsResolvesTheBuiltinDialFiles(t *testing.T) {
	relay := newFakeRelay(t, "pair")
	home := t.TempDir()
	cfg := &config.Config{Paths: config.Paths{Home: home}}
	own := filepath.Join(t.TempDir(), "own-ca.pem")
	cfg.Swarm.Join = []config.SwarmJoin{
		{URL: relay.ts.URL, Name: "node-a", PairingToken: "pair", AdvertiseURL: "http://a:1", Dial: config.SwarmDialConfig{Auto: true}},
		{URL: relay.ts.URL, Name: "node-b", PairingToken: "pair", AdvertiseURL: "http://b:1", Dial: config.SwarmDialConfig{Auto: true, CAFile: own}},
	}
	// The files exist before anything dials: coddy serve makes them at start, and a join reads them when it is built.
	if _, err := config.EnsureBuiltinTLS(cfg, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(pki.PathsIn(pki.Dir(home)).CA)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := StartJoins(context.Background(), cfg, StartJoinsOptions{
		Kind: KindAgent, Handler: http.NotFoundHandler(), Log: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Stop()
	b := pki.PathsIn(pki.Dir(home))
	if got := set.Clients()[0].opts.Dial; got.CAFile != b.Bundle || got.CertFile != b.ClientCert || got.KeyFile != b.ClientKey {
		t.Errorf("auto dial options = %+v, want the bundle and the client pair of %s", got, home)
	}
	if got := set.Clients()[1].opts.Dial; got.CAFile != own || got.CertFile != b.ClientCert {
		t.Errorf("a CA named by hand must win and leave the pair built-in: %+v", got)
	}
}
