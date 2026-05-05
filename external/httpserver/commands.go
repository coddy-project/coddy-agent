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

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// CommandDeps wires coddy main helpers into the http subcommand without an import cycle.
type CommandDeps struct {
	NewServerRef func(**acp.Server, *config.Config) acp.UpdateSender
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
	disableSession := fs.Bool("disable-session", false, "do not write sessions to disk (in-memory only)")
	persistedSession := fs.String("session-id", "", "optional session id for new sessions (folder name)")
	host := fs.String("H", "0.0.0.0", "bind address for HTTP")
	port := fs.String("P", "12345", "listen port for HTTP")
	fs.StringVar(host, "host", "0.0.0.0", "bind address for HTTP (alias of -H)")
	fs.StringVar(port, "port", "12345", "listen port (alias of -P)")

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

	log.Info("starting HTTP server", "version", version.Get())

	var store *session.FileStore
	if *disableSession {
		log.Info("session persistence disabled", "reason", "disable-session flag")
	} else {
		store, err = deps.OpenStore(*sessionsRoot, cfg)
		if err != nil {
			return err
		}
		if store != nil {
			log.Info("session persistence enabled", "root", store.Root)
		}
	}

	var srv *acp.Server
	ref := deps.NewServerRef(&srv, cfg)
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		loop := agent.NewAgent(cfg, st, snd, log)
		return loop.Run(ctx, prompt)
	}
	mgr := session.NewManager(cfg, ref, runner, log, paths.CWD, store)
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

	log.Info("listening", "addr", listenAddr)
	s := New(cfg, mgr, log, paths.CWD)
	return ListenAndServe(listenAddr, s)
}
