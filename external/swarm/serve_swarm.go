//go:build swarm

package swarm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// Available reports whether this binary can run the relay.
const Available = true

// shutdownGrace bounds the wait for in-flight proxied requests on stop.
const shutdownGrace = 10 * time.Second

// generatedTokenFile is where a relay without a client token leaves the one it
// generated for the run, under the coddy home and readable by the owner only.
const generatedTokenFile = "swarm-token"

// announceGeneratedToken tells the operator a generated client token. The value
// goes to an owner-only file, and to stderr only when that is a terminal: a
// relay in the background has its stderr in logs/serve.log, and under systemd in
// the journal, which outlive the run and have more readers than the operator.
func announceGeneratedToken(home, token string, log *slog.Logger, stderr io.Writer, terminal bool) {
	path := filepath.Join(home, generatedTokenFile)
	if err := config.AtomicWriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		log.Warn("swarm: no client token configured, and the generated one could not be saved; set swarm.auth_token", "file", path, "error", err)
	} else {
		log.Warn("swarm: no client token configured, generated one for this run", "file", path)
	}
	if terminal {
		_, _ = fmt.Fprintf(stderr, "swarm: generated client token for this run: %s\n", token)
	}
}

// Serve runs the relay until ctx is cancelled.
func Serve(ctx context.Context, opts Options) error {
	if opts.Cfg == nil || opts.Log == nil {
		return errors.New("swarm: Cfg and Log are required")
	}
	cfg, log := opts.Cfg, opts.Log

	srv, err := New(cfg, log)
	if err != nil {
		return err
	}
	defer srv.Close()
	srv.EnableSettings(opts.Live, opts.Install)
	// Status surfaces (the metrics exporter) count the nodes this relay knows
	// for as long as it runs.
	defer swarmdto.PublishRelay(srv.Registry().List)()

	// Resolving the client credential is a security decision, not a
	// convenience: a relay holds every node's credential, so an open one hands
	// its whole fleet to anybody who can reach it.
	bindHost, _, _ := net.SplitHostPort(opts.ListenAddr)
	tokens := append([]string(nil), opts.ExtraAuthTokens...)
	if len(tokens) > 0 {
		srv.SetExtraAuthTokens(tokens)
	}
	if len(tokens) == 0 && strings.TrimSpace(cfg.Swarm.AuthToken) == "" {
		if !isLoopbackBind(bindHost) && !cfg.Swarm.AllowInsecure {
			return fmt.Errorf("swarm: refusing to bind %s without a client token: a relay reaches every node with that node's own credential, so an open one exposes the whole fleet (set swarm.auth_token, --swarm-auth-token, %s, or --swarm-allow-insecure to override)", opts.ListenAddr, TokenEnvVar)
		}
		// Even on loopback an open relay lends its authority to every local
		// process, so one is generated rather than left absent.
		generated, gerr := GenerateToken()
		if gerr != nil {
			return gerr
		}
		srv.SetExtraAuthTokens([]string{generated})
		announceGeneratedToken(opts.Home, generated, log, os.Stderr, term.IsTerminal(int(os.Stderr.Fd())))
		defer func() { _ = os.Remove(filepath.Join(opts.Home, generatedTokenFile)) }()
	}
	if !cfg.Swarm.RegistrationOpen() {
		log.Warn("swarm: registration is closed, no node can join (set swarm.pairing_tokens or --swarm-pairing-token)")
	}

	// A relay joins its own parents exactly the way an agent joins a relay.
	// That symmetry is the whole of relay chaining: nothing here knows or cares
	// how deep the chain goes.
	joins, err := swarmdto.StartJoins(ctx, cfg, swarmdto.StartJoinsOptions{
		Kind: swarmdto.KindRelay, Home: opts.Home, Handler: srv.Handler(),
		// A relay registers under the identity it reports on /swarm/info, so a
		// parent's view of the topology joins up with this relay's own.
		InstanceUUID: srv.UUID(), Log: log,
	})
	if err != nil {
		return err
	}
	defer joins.Stop()

	log.Info("swarm relay listening", "addr", opts.ListenAddr, "uuid", srv.UUID(),
		"tls", cfg.Swarm.TLS.Enabled(), "nodes", srv.Registry().Len(), "parents", len(cfg.Swarm.Join))

	server := httpx.NewServer(opts.ListenAddr, srv.Handler())
	errs := make(chan error, 1)
	go func() {
		if cfg.Swarm.TLS.Enabled() {
			errs <- server.ListenAndServeTLS(cfg.Swarm.TLS.CertFile, cfg.Swarm.TLS.KeyFile)
			return
		}
		errs <- server.ListenAndServe()
	}()

	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); errors.Is(err, context.DeadlineExceeded) {
			log.Warn("swarm shutdown timed out, closing anyway", "grace", shutdownGrace)
			_ = server.Close()
		}
		return nil
	}
}

func isLoopbackBind(host string) bool {
	h := strings.TrimSpace(host)
	if h == "" {
		// An empty bind means every interface.
		return false
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
