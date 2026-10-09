//go:build http

package httpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/agent"
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
	llm.LogDevinAuthNotices(log, opts.Cfg)
	opts.Cfg.LogUnsentModelSettings(log)
	llm.LogNeuralDeepAuthNotices(log, opts.Cfg)

	s := New(opts.Cfg, opts.Mgr, log, opts.DefaultCWD)
	s.SetDetachedPrompts(opts.DetachedPrompts)
	s.SetExtraAuthTokens(opts.ExtraAuthTokens)
	s.SetExtraSwarmTokens(opts.ExtraSwarmTokens)
	if err := s.SetExtraLogin(opts.ExtraLogin.User, opts.ExtraLogin.Password); err != nil {
		return fmt.Errorf("httpserver: %s / %s: %w", LoginUserEnvVar, LoginPasswordEnvVar, err)
	}
	// A shared-model token must open the shared-model routes and nothing else, so it
	// can never be the same as a token of another class: refused at start, with
	// the flag and environment tokens in view that the loader cannot see.
	if err := s.checkTokenClasses(s.activeCfg()); err != nil {
		return fmt.Errorf("httpserver: %w", err)
	}
	// A form nobody can pass is refused here, in the terminal that typed the
	// command, rather than discovered by an operator staring at a sign-in screen
	// that rejects the password they are sure of.
	if pol := s.loginPolicyNow(); pol.broken {
		return errors.New("httpserver.login.enable is true but no account is configured: " +
			"set httpserver.login.user and password_hash (`coddy serve set-password`), " +
			"or " + LoginUserEnvVar + " / " + LoginPasswordEnvVar)
	}
	if opts.OnServer != nil {
		opts.OnServer(s)
		defer opts.OnServer(nil)
	}
	// A notifying task wakes the agent. In `coddy serve` the
	// runtime owns the waker and this server is where a woken turn runs when
	// no chat owns the session; on its own the server attaches a waker itself.
	if opts.Wakes != nil {
		withdrawWakes := opts.Wakes.AddWakeSurface(s, agent.WakeHost)
		defer withdrawWakes()
	} else {
		s.AttachBackgroundWaker()
	}

	// From here on the server's own configuration is the one to read: New
	// caught up with any replacement the manager made after opts.Cfg was
	// taken, which the warnings and the relays joined below have to see too.
	cfg := s.activeCfg()
	tokenOn := len(cfg.HTTPServer.EffectiveAuthTokens()) > 0 || len(opts.ExtraAuthTokens) > 0
	loginOn := s.loginPolicyNow().enabled
	sharedOn := len(cfg.HTTPServer.EffectiveSharedTokens()) > 0
	// A shared-model token closes the gate too: every route but the shared-model
	// routes is then refused to every caller.
	authOn := tokenOn || loginOn || sharedOn
	effHost, _, _ := net.SplitHostPort(opts.ListenAddr)
	if !authOn && !cfg.HTTPServer.AllowInsecure && !isLoopbackHost(effHost) {
		log.Warn("HTTP API is reachable without authentication",
			"addr", opts.ListenAddr,
			"hint", "sign-in for the browser: `coddy serve set-password`, or "+LoginUserEnvVar+" / "+LoginPasswordEnvVar+"; "+
				"a token for API clients: httpserver.auth_token / --auth-token / "+TokenEnvVar+"; "+
				"httpserver.allow_insecure: true silences this")
	}
	// CORS that admits pages nobody listed - allow_loopback or "*" - lets a
	// page in a browser read this API, and without a credential that is every
	// page served from the browser's own machine (a dev server, a desktop app)
	// or, with "*", every page anywhere. The bind address does not matter: a
	// loopback bind is exactly where such pages reach.
	if !authOn && !cfg.HTTPServer.AllowInsecure && cfg.HTTPServer.CORS.OpenToUnlistedOrigins() {
		log.Warn("CORS admits pages nobody listed and the API asks for no credential",
			"addr", opts.ListenAddr,
			"hint", "httpserver.cors.allow_loopback or allowed_origins: [\"*\"] is only as safe as the credential behind the API: "+
				"set a token (httpserver.auth_token / --auth-token / "+TokenEnvVar+") or run `coddy serve set-password`; "+
				"httpserver.allow_insecure: true silences this")
	}
	// The two credentials are not interchangeable: a browser signs in at the
	// form, and everything that is not a browser - `coddy --remote`, `coddy acp
	// --remote`, a swarm relay reaching this node, the Python harnesses - still
	// presents a bearer token. Closing the door with only a password would lock
	// those out on the next call they make, so it is said plainly here.
	if loginOn && !tokenOn {
		log.Info("web sign-in is on and no bearer token is set",
			"note", "API clients (coddy --remote, coddy acp --remote, a swarm relay mounting this node, scripts) authenticate with a token, not the form",
			"hint", "set httpserver.auth_token / --auth-token / "+TokenEnvVar+" if anything but a browser talks to this server")
	}

	if sharedOn && !tokenOn && !loginOn {
		log.Info("only shared-model tokens are configured: every API route except the shared-model routes is closed to every caller, and the web UI cannot sign in",
			"hint", "set httpserver.auth_token / --auth-token / "+TokenEnvVar+" or httpserver.login to administer this node over its API or its web UI")
	}
	if len(cfg.SharedModelEntries()) > 0 && !authOn && !cfg.HTTPServer.AllowInsecure {
		log.Warn("models are shared and no credential is configured: the shared-model routes answer 403 until one is",
			"hint", "set httpserver.shared_models.tokens (a token that opens only the LLM routes) or httpserver.auth_token; httpserver.allow_insecure: true is for an API that is open on purpose")
	}

	// Joining a relay is what makes this agent reachable from a swarm. It runs
	// alongside the listener rather than before it, because the relay may dial
	// straight back and should find the API already up.
	stopSwarm := startSwarmJoins(ctx, cfg, opts.Home, s.Handler(), log)
	defer stopSwarm()

	srv := httpx.NewServer(opts.ListenAddr, s.Handler())
	tlsOn := cfg.HTTPServer.TLS.Enabled()
	files := cfg.HTTPListenerFiles()
	if tlsOn {
		clientTLS, err := listenerTLS(files.ClientCA)
		if err != nil {
			return err
		}
		srv.TLSConfig = clientTLS
		// HTTP/1.1 only, like the relay's mount and a coddy row's client: a shared call rides a connection of its
		// own, which is what lets the per-call user timeout free the slot of a peer that vanished.
		srv.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
	}
	errs := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", opts.ListenAddr, "auth", authOn, "tls", tlsOn,
			"client_ca", files.ClientCA != "", "builtin", cfg.HTTPServer.TLS.Auto)
		if tlsOn {
			errs <- srv.ListenAndServeTLS(files.Cert, files.Key)
			return
		}
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
