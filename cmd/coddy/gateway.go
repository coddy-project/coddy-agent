//go:build gateway || gateway.telegram

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/EvilFreelancer/coddy-agent/external/gateway"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

func runGateway(args []string) error {
	fs := flag.NewFlagSet("gateway", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to config.yaml")
	homeDir := fs.String("home", "", "agent state directory (CODDY_HOME, default ~/.coddy)")
	gwCWD := fs.String("cwd", "", "default session working directory")
	sessionsRoot := fs.String("sessions-dir", "", "sessions root directory")
	logLevel := fs.String("log-level", "", "debug|info|warn|error")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage of gateway:\n")
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
		CWD:    strings.TrimSpace(*gwCWD),
		Config: strings.TrimSpace(*cfgPath),
	}
	paths, err := config.Resolve(cli)
	if err != nil {
		return err
	}
	if err := ensureCoddyHomeLayout(paths.Home); err != nil {
		return err
	}

	cfg, err := config.LoadFromCLI(cli)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if !cfg.Gateways.Telegram.Enabled {
		return fmt.Errorf("no gateway enabled; set gateways.telegram.enabled: true in config")
	}

	cfg.Logger.ApplyOverrides(config.LoggerCLIOverrides{Level: strings.TrimSpace(*logLevel)})
	log, logCloser, err := logger.New(cfg.Logger)
	if err != nil {
		return fmt.Errorf("log: %w", err)
	}
	defer func() { _ = logCloser.Close() }()

	log.Info("starting gateway", "version", version.Get())

	store, err := openSessionStore(*sessionsRoot, cfg)
	if err != nil {
		return err
	}

	// Build a null ACP sender (gateway uses its own per-message senders).
	nullSender := &nullUpdateSender{}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		loop := agent.NewAgent(cfg, st, snd, log)
		return loop.Run(ctx, prompt)
	}
	mgr := session.NewManager(cfg, nullSender, runner, log, paths.CWD, store)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("gateway hub starting")
	gateway.Start(ctx, cfg, mgr, log, paths.CWD)
	log.Info("gateway hub stopped")
	return nil
}

// nullUpdateSender is used as the manager's default server (gateway sessions use per-message senders).
type nullUpdateSender struct{}

func (n *nullUpdateSender) SendSessionUpdate(_ string, _ interface{}) error { return nil }
func (n *nullUpdateSender) RequestPermission(_ context.Context, _ acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}
func (n *nullUpdateSender) RequestQuestion(_ context.Context, _ acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}
