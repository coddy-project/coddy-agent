package swarm

import (
	"context"
	"net/http"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The heartbeat and tunnel use the proxy configured on the join.
func TestStartJoinsCarriesTheDialProxy(t *testing.T) {
	relay := newFakeRelay(t, "pair")
	cfg := &config.Config{}
	cfg.Swarm.Join = []config.SwarmJoin{{
		URL: relay.ts.URL, Name: "node-a", PairingToken: "pair", AdvertiseURL: "http://a:1",
		Dial: config.SwarmDialConfig{Proxy: "http://127.0.0.1:1"},
	}}
	set, err := StartJoins(context.Background(), cfg, StartJoinsOptions{
		Kind: KindAgent, Handler: http.NotFoundHandler(), Log: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Stop()
	if got := set.Clients()[0].opts.Dial.Proxy; got != cfg.Swarm.Join[0].Dial.Proxy {
		t.Fatalf("dial proxy = %q", got)
	}
}
