//go:build swarm

package swarm_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	swarmrelay "github.com/EvilFreelancer/coddy-agent/external/swarm"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// A second process that shares the first one's lease secret and carries no
// token takes the relay's stored token away at its next heartbeat: the relay
// holds exactly what the owner last sent.
func TestAHeartbeatWithoutATokenErasesTheRelaysCopy(t *testing.T) {
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.Name = "test-relay"
	cfg.Swarm.PairingTokens = []string{"pair"}
	cfg.Swarm.AuthToken = "client"
	relay, err := swarmrelay.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(relay.Handler())
	t.Cleanup(ts.Close)

	store := &swarm.MemorySecretStore{}
	opts := swarm.JoinOptions{
		RelayURL:     ts.URL,
		Name:         "nas02",
		PairingToken: "pair",
		AdvertiseURL: "http://127.0.0.1:12345",
		NodeToken:    "node-token",
		Secrets:      store,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	first, err := swarm.NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n, _ := relay.Registry().Node("nas02"); n.Token != "node-token" {
		t.Fatalf("relay holds %q after the claim, want node-token", n.Token)
	}

	opts.NodeToken = ""
	second, err := swarm.NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n, _ := relay.Registry().Node("nas02"); n.Token != "" {
		t.Fatalf("relay still holds %q after a heartbeat with no token", n.Token)
	}
}
