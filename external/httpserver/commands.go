//go:build http

package httpserver

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// CommandDeps wires coddy main helpers into the http subcommand without an import cycle.
type CommandDeps struct {
	NewServerRef func(**acp.Server, *config.Config, func() *config.Config) acp.UpdateSender
	EnsureHome   func(string) error
	OpenStore    func(string, *config.Config) (*session.FileStore, error)
}

// Run executes the coddy http subcommand.
func Run(args []string, deps CommandDeps) error {
	if deps.NewServerRef == nil || deps.EnsureHome == nil || deps.OpenStore == nil {
		return fmt.Errorf("httpserver: incomplete CommandDeps")
	}

	fs := flag.NewFlagSet("http", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to config.yaml (CODDY_CONFIG, else <home>/config.yaml or legacy search paths)")
	logLevel := fs.String("log-level", "", "debug|info|warn|error (default from config)")
	logOutput := fs.String("log-output", "", "stdout|stderr|file|both (default from config)")
	logFile := fs.String("log-file", "", "log file path when output includes file (default from config)")
	logFormat := fs.String("log-format", "", "text|json (default from config)")
	homeDir := fs.String("home", "", "agent state directory (CODDY_HOME, default ~/.coddy)")
	httpCWD := fs.String("cwd", "", "default session cwd when the client omits cwd (CODDY_CWD, default process cwd)")
	sessionsRoot := fs.String("sessions-dir", "", "sessions root (empty uses config sessions.dir or ~/.coddy/sessions)")
	persistedSession := fs.String("session-id", "", "optional session id for new sessions (folder name)")
	host := fs.String("H", "0.0.0.0", "bind address for HTTP")
	port := fs.String("P", "12345", "listen port for HTTP")
	fs.StringVar(host, "host", "0.0.0.0", "bind address for HTTP (alias of -H)")
	fs.StringVar(port, "port", "12345", "listen port (alias of -P)")
	authToken := fs.String("auth-token", "", "bearer token required on /v1/* and /coddy/* routes (else CODDY_HTTP_TOKEN, else httpserver.auth_token). Empty = no auth")
	schedulerEnabled := fs.Bool("scheduler-enabled", false, "set scheduler.enabled=true in this process (build with -tags scheduler)")
	skillsAutoDiscovery := fs.Bool(config.SkillsAutoDiscoveryFlagName, true, "model-driven skill auto-discovery (load_skill tool); pass =false to disable and override config")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage of http:\n")
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
		CWD:    strings.TrimSpace(*httpCWD),
		Config: strings.TrimSpace(*cfgPath),
	}
	paths, err := config.Resolve(cli)
	if err != nil {
		return err
	}
	if err := deps.EnsureHome(paths.Home); err != nil {
		return err
	}

	cfg, err := config.LoadFromCLI(cli)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if *schedulerEnabled {
		cfg.Scheduler.Enabled = true
	}
	config.ApplySkillsAutoDiscoveryFlag(fs, cfg, skillsAutoDiscovery)
	if err := cfg.Scheduler.Validate(cfg); err != nil {
		return fmt.Errorf("scheduler: %w", err)
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

	log.Info("starting HTTP server", "version", version.Get(), "config", paths.ConfigPath, "workspace", paths.CWD)

	if cfg.SchedulerEffectiveEnabled() {
		scheduler.Start(context.Background(), cfg, log, paths.CWD)
	}

	store, err := deps.OpenStore(*sessionsRoot, cfg)
	if err != nil {
		return err
	}
	log.Info("session persistence enabled", "root", store.Root)

	var srv *acp.Server
	var mgr *session.Manager
	live := func() *config.Config {
		if mgr != nil {
			return mgr.Cfg()
		}
		return cfg
	}
	ref := deps.NewServerRef(&srv, cfg, live)
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		c := live()
		loop := agent.NewAgent(c, st, snd, log)
		return loop.Run(ctx, prompt)
	}
	mgr = session.NewManager(cfg, ref, runner, log, paths.CWD, store)
	if pid := strings.TrimSpace(*persistedSession); pid != "" {
		if err := session.ValidateFolderSessionID(pid); err != nil {
			return fmt.Errorf("--session-id: %w", err)
		}
		mgr.SetPreferredSessionID(pid)
	}

	hostStr := strings.TrimSpace(*host)
	portStr := strings.TrimSpace(*port)
	listenAddr := net.JoinHostPort(hostStr, portStr)
	if hostStr == "0.0.0.0" && portStr == "12345" {
		listenAddr = net.JoinHostPort(cfg.HTTPServer.DefaultListenHost(), cfg.HTTPServer.DefaultListenPortString())
	}

	s := New(cfg, mgr, log, paths.CWD)

	// Out-of-band bearer tokens (--auth-token, then CODDY_HTTP_TOKEN) enable auth without
	// writing the secret into config.yaml and survive PUT /coddy/config hot reloads.
	var extraTokens []string
	if t := strings.TrimSpace(*authToken); t != "" {
		extraTokens = append(extraTokens, t)
	}
	if t := strings.TrimSpace(os.Getenv("CODDY_HTTP_TOKEN")); t != "" {
		extraTokens = append(extraTokens, t)
	}
	s.SetExtraAuthTokens(extraTokens)

	authOn := len(cfg.HTTPServer.EffectiveAuthTokens()) > 0 || len(extraTokens) > 0
	effHost, _, _ := net.SplitHostPort(listenAddr)
	if !authOn && !cfg.HTTPServer.AllowInsecure && !isLoopbackHost(effHost) {
		log.Warn("HTTP API is reachable without authentication",
			"addr", listenAddr,
			"hint", "set httpserver.auth_token / --auth-token / CODDY_HTTP_TOKEN, or httpserver.allow_insecure: true to silence")
	}
	log.Info("listening", "addr", listenAddr, "auth", authOn)
	return ListenAndServe(listenAddr, s)
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
