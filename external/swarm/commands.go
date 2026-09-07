//go:build swarm

package swarm

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// TokenEnvVar is where the relay looks for its client credential when no flag
// carries one, so a token need not be written into config.yaml.
const TokenEnvVar = "CODDY_SWARM_TOKEN"

// PairingEnvVar is the registration credential's environment fallback.
const PairingEnvVar = "CODDY_SWARM_PAIRING_TOKEN"

// CommandDeps are the few things the relay needs from the CLI layer.
type CommandDeps struct {
	// EnsureHome prepares the coddy home directory.
	EnsureHome func(home string) error
}

// Run executes the coddy swarm subcommand.
func Run(args []string, deps CommandDeps) error {
	fs := flag.NewFlagSet("swarm", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to config.yaml (CODDY_CONFIG, else <home>/config.yaml)")
	logLevel := fs.String("log-level", "", "debug|info|warn|error (default from config)")
	logOutput := fs.String("log-output", "", "stdout|stderr|file|both (default from config)")
	logFile := fs.String("log-file", "", "log file path when output includes file (default from config)")
	logFormat := fs.String("log-format", "", "text|json (default from config)")
	homeDir := fs.String("home", "", "agent state directory (CODDY_HOME, default ~/.coddy)")
	host := fs.String("H", "", "bind address (default swarm.host, else 0.0.0.0)")
	port := fs.String("P", "", "listen port (default swarm.port, else 12346)")
	fs.StringVar(host, "host", "", "bind address (alias of -H)")
	fs.StringVar(port, "port", "", "listen port (alias of -P)")
	authToken := fs.String("auth-token", "", "bearer token clients must present (else "+TokenEnvVar+", else swarm.auth_token)")
	pairingToken := fs.String("pairing-token", "", "credential nodes must present to register (else "+PairingEnvVar+", else swarm.pairing_tokens)")
	allowInsecure := fs.Bool("allow-insecure", false, "permit binding off loopback without a client token")

	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage of swarm:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	cli := config.CLIPaths{
		Home:   strings.TrimSpace(*homeDir),
		Config: strings.TrimSpace(*cfgPath),
	}
	paths, err := config.Resolve(cli)
	if err != nil {
		return err
	}
	if deps.EnsureHome != nil {
		if err := deps.EnsureHome(paths.Home); err != nil {
			return err
		}
	}
	cfg, err := config.LoadFromCLI(cli)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if *allowInsecure {
		cfg.Swarm.AllowInsecure = true
	}
	if t := firstNonEmpty(*pairingToken, os.Getenv(PairingEnvVar)); t != "" {
		cfg.Swarm.PairingTokens = append(cfg.Swarm.PairingTokens, t)
	}
	cfg.Swarm.Normalize()
	if err := cfg.Swarm.Validate(); err != nil {
		return err
	}

	cfg.Logger.ApplyOverrides(config.LoggerCLIOverrides{
		Level:  strings.TrimSpace(*logLevel),
		Output: strings.TrimSpace(*logOutput),
		File:   strings.TrimSpace(*logFile),
		Format: strings.TrimSpace(*logFormat),
	})
	log, logCloser, err := logger.New(cfg.Logger)
	if err != nil {
		return fmt.Errorf("log: %w", err)
	}
	defer func() { _ = logCloser.Close() }()

	bindHost := firstNonEmpty(strings.TrimSpace(*host), cfg.Swarm.EffectiveHost())
	bindPort := firstNonEmpty(strings.TrimSpace(*port), strconv.Itoa(cfg.Swarm.EffectivePort()))
	addr := net.JoinHostPort(bindHost, bindPort)

	srv, err := New(cfg, log)
	if err != nil {
		return err
	}

	// Resolving the client credential is a security decision, not a
	// convenience: a relay holds every node's credential, so an open one hands
	// its whole fleet to anybody who can reach it.
	token := firstNonEmpty(strings.TrimSpace(*authToken), strings.TrimSpace(os.Getenv(TokenEnvVar)))
	if token != "" {
		srv.SetExtraAuthTokens([]string{token})
	}
	hasToken := token != "" || strings.TrimSpace(cfg.Swarm.AuthToken) != ""
	if !hasToken {
		if !isLoopbackBind(bindHost) && !cfg.Swarm.AllowInsecure {
			return fmt.Errorf("swarm: refusing to bind %s without a client token: a relay reaches every node with that node's own credential, so an open one exposes the whole fleet (set swarm.auth_token, --auth-token, %s, or --allow-insecure to override)", addr, TokenEnvVar)
		}
		// Even on loopback an open relay lends its authority to every local
		// process, so one is generated rather than left absent.
		generated, gerr := GenerateToken()
		if gerr != nil {
			return gerr
		}
		srv.SetExtraAuthTokens([]string{generated})
		log.Warn("swarm: no client token configured, generated one for this run", "token", generated)
		fmt.Fprintf(os.Stderr, "swarm: generated client token for this run: %s\n", generated)
	}
	if !cfg.Swarm.RegistrationOpen() {
		log.Warn("swarm: registration is closed, no node can join (set swarm.pairing_tokens or --pairing-token)")
	}

	// A relay joins its own parents exactly the way an agent joins a relay.
	// That symmetry is the whole of relay chaining: nothing here knows or cares
	// how deep the chain goes.
	joins, err := swarmdto.StartJoins(context.Background(), cfg, swarmdto.StartJoinsOptions{
		Kind: swarmdto.KindRelay, Home: paths.Home, Handler: srv.Handler(),
		// A relay registers under the identity it reports on /swarm/info, so a
		// parent's view of the topology joins up with this relay's own.
		InstanceUUID: srv.UUID(), Log: log,
	})
	if err != nil {
		return err
	}
	defer joins.Stop()

	log.Info("swarm relay listening", "addr", addr, "uuid", srv.UUID(), "tls", cfg.Swarm.TLS.Enabled(), "nodes", srv.Registry().Len(), "parents", len(cfg.Swarm.Join))

	server := httpx.NewServer(addr, srv.Handler())
	if cfg.Swarm.TLS.Enabled() {
		return server.ListenAndServeTLS(cfg.Swarm.TLS.CertFile, cfg.Swarm.TLS.KeyFile)
	}
	return server.ListenAndServe()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
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
