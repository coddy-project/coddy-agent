package swarm

import (
	"context"
	"net/http"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
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
