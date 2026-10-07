//go:build http

package httpserver

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// lockedBuffer is a log sink the test reads while the server writes to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func serveOptions(fx *sharedFixture, log *slog.Logger) Options {
	return Options{Cfg: fx.cfg, Mgr: fx.srv.mgr, Log: log, DefaultCWD: fx.home, Home: fx.home, ListenAddr: "0.0.0.0:0"}
}

// A shared-model token that is the same as a token given by flag is refused at
// start, naming the key and never the value.
func TestServeRefusesASharedTokenThatIsAlsoAFlagToken(t *testing.T) {
	fx := newSharedFixture(t)
	opts := serveOptions(fx, slog.New(slog.NewTextHandler(&lockedBuffer{}, nil)))
	opts.ExtraAuthTokens = []string{sharedTestSharedTok}
	err := Serve(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "httpserver.shared_models.tokens[0]") || strings.Contains(err.Error(), sharedTestSharedTok) {
		t.Fatalf("Serve: %v", err)
	}
	opts.ExtraAuthTokens = nil
	opts.ExtraSwarmTokens = []string{sharedTestSharedTok}
	if err := Serve(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "swarm") {
		t.Fatalf("a shared token equal to a swarm token: %v", err)
	}
}

// A shared-model token counts as a credential for the start-up warnings: a node
// that holds only shared tokens is not "reachable without authentication".
func TestServeCountsSharedTokensAsAuthentication(t *testing.T) {
	run := func(t *testing.T, mut func(*config.Config)) string {
		fx := newSharedFixture(t, withSharedConfig(mut))
		logs := &lockedBuffer{}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- Serve(ctx, serveOptions(fx, slog.New(slog.NewTextHandler(logs, nil)))) }()
		waitFor(t, "the server to listen", func() bool { return strings.Contains(logs.String(), "listening") })
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatal("Serve did not return")
		}
		return logs.String()
	}
	only := run(t, sharedOnly)
	if strings.Contains(only, "reachable without authentication") || !strings.Contains(only, "auth=true") {
		t.Fatalf("a share-only node warned about no authentication:\n%s", only)
	}
	if !strings.Contains(only, "only shared-model tokens are configured") {
		t.Fatalf("a share-only node did not say its API is closed:\n%s", only)
	}
	open := run(t, noCredentials)
	if !strings.Contains(open, "reachable without authentication") || !strings.Contains(open, "the shared-model routes answer 403") {
		t.Fatalf("an open node with shared models did not warn:\n%s", open)
	}
}
