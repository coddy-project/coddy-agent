//go:build swarm

package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

// A relay a reload turns on listens where that reload says. The address used
// to be read once, when the process started, so a relay enabled later from the
// settings bound the port the file had then, while the supervisor recorded the
// new one and never asked for the restart that would have moved it (issue
// #401).
func TestARelayTurnedOnByAReloadListensWhereTheReloadSays(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt := &serve.Runtime{Log: log}
	var relay serve.Subsystem
	for _, sub := range subsystems(rt, subsystemDeps{
		httpListenAddr: func(*config.Config) string { return "" },
		swarmListenAddr: func(c *config.Config) string {
			return net.JoinHostPort(c.Swarm.Host, strconv.Itoa(c.Swarm.Port))
		},
		home:    t.TempDir(),
		metrics: newMetricsWiring(rt),
	}) {
		if sub.Kind == serve.KindSwarm {
			relay = sub
		}
	}
	// Something else keeps the process up while the relay is off, the way the
	// HTTP server does in a real one.
	keeper := serve.Subsystem{
		Kind: serve.KindScheduler, Available: true,
		Enabled: func(*config.Config) bool { return true },
		Run: func(ctx context.Context, _ *config.Config) error {
			<-ctx.Done()
			return nil
		},
	}
	first, second := freeLoopbackPort(t), freeLoopbackPort(t)
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.Port = first
	cfg.Swarm.AuthToken = "client-secret"

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	reloads := make(chan *config.Config, 1)
	sup := serve.NewSupervisor(log, []serve.Subsystem{keeper, relay})
	go func() { done <- sup.Run(ctx, cfg, reloads) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("the supervisor did not stop")
		}
	}()

	next := *cfg
	next.Swarm.Enabled = true
	next.Swarm.Port = second
	reloads <- &next
	if !eventuallyListening(t, second) {
		t.Fatalf("the relay is not listening on the port the reload gave it (%d)", second)
	}
	if listening(first) {
		t.Errorf("the relay listens on the port the process started with (%d)", first)
	}
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func listening(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func eventuallyListening(t *testing.T, port int) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if listening(port) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
