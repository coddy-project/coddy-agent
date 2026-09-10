//go:build http

package httpserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// Available reports whether this binary can serve the HTTP API.
const Available = true

// shutdownGrace bounds the wait for in-flight requests once the process is
// stopping. Streaming turns can run far longer than this, so the deadline is
// what stops a browser holding an SSE connection from keeping the process alive.
const shutdownGrace = 10 * time.Second

// Serve runs the HTTP API until ctx is cancelled.
//
// Everything it needs is already built by the caller: the configuration, the
// logger and the session manager are shared with whatever else this process is
// running, which is what lets a conversation started in a chat be opened in a
// browser while it is still going.
func Serve(ctx context.Context, opts Options) error {
	if opts.Cfg == nil || opts.Mgr == nil || opts.Log == nil {
		return errors.New("httpserver: Cfg, Mgr and Log are required")
	}
	log := opts.Log
	llm.LogCodexAuthNotices(log, opts.Cfg)
	llm.LogNeuralDeepAuthNotices(log, opts.Cfg)

	s := New(opts.Cfg, opts.Mgr, log, opts.DefaultCWD)
	s.SetExtraAuthTokens(opts.ExtraAuthTokens)
	if opts.OnServer != nil {
		opts.OnServer(s)
		defer opts.OnServer(nil)
	}

	authOn := len(opts.Cfg.HTTPServer.EffectiveAuthTokens()) > 0 || len(opts.ExtraAuthTokens) > 0
	effHost, _, _ := net.SplitHostPort(opts.ListenAddr)
	if !authOn && !opts.Cfg.HTTPServer.AllowInsecure && !isLoopbackHost(effHost) {
		log.Warn("HTTP API is reachable without authentication",
			"addr", opts.ListenAddr,
			"hint", "set httpserver.auth_token / --auth-token / CODDY_HTTP_TOKEN, or httpserver.allow_insecure: true to silence")
	}

	// Joining a relay is what makes this agent reachable from a swarm. It runs
	// alongside the listener rather than before it, because the relay may dial
	// straight back and should find the API already up.
	stopSwarm := startSwarmJoins(ctx, opts.Cfg, opts.Home, s.Handler(), log)
	defer stopSwarm()

	srv := httpx.NewServer(opts.ListenAddr, s.Handler())
	errs := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", opts.ListenAddr, "auth", authOn)
		errs <- srv.ListenAndServe()
	}()

	select {
	case err := <-errs:
		s.Drain()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		err := srv.Shutdown(shutdownCtx)
		// Drain after the listener is closed, so nothing new can enter the
		// pools this is emptying.
		s.Drain()
		if errors.Is(err, context.DeadlineExceeded) {
			log.Warn("HTTP shutdown timed out, closing anyway", "grace", shutdownGrace)
			_ = srv.Close()
			return nil
		}
		return err
	}
}

// isLoopbackHost reports whether a bind host only accepts local connections.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "", "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
