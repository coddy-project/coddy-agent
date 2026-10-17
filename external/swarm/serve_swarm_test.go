//go:build swarm

package swarm

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// A running relay publishes its registry to the status surfaces of the process
// (the metrics exporter counts its nodes) and withdraws it when it stops.
func TestServePublishesTheRelayRegistryWhileItRuns(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	cfg := &config.Config{}
	cfg.Swarm.Enabled = true
	cfg.Swarm.AuthToken = "client-token"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Options{Cfg: cfg, Log: slog.New(slog.DiscardHandler), Home: t.TempDir(), ListenAddr: addr})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if nodes, ok := swarmdto.RelayNodes(); ok {
			if len(nodes) != 0 {
				t.Errorf("a fresh relay knows nodes: %+v", nodes)
			}
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("the relay never published its registry: %v", <-done)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if _, ok := swarmdto.RelayNodes(); ok {
		t.Fatal("a stopped relay is still published")
	}
}
