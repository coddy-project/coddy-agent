// Command coddy is the Coddy Agent CLI (ACP server and skills).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/react"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// serverRef breaks the cyclic dependency between acp.Server and session.Manager.
type serverRef struct {
	p **acp.Server
}

func (r *serverRef) SendSessionUpdate(sessionID string, update interface{}) error {
	s := *r.p
	if s == nil {
		return fmt.Errorf("acp server not initialized")
	}
	return s.SendSessionUpdate(sessionID, update)
}

func (r *serverRef) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s := *r.p
	if s == nil {
		return nil, fmt.Errorf("acp server not initialized")
	}
	return s.RequestPermission(ctx, params)
}

func main() {
	if len(os.Args) >= 2 {
		a := os.Args[1]
		if a == "-v" || a == "--version" {
			fmt.Println(version.Get())
			os.Exit(0)
		}
	}

	args := os.Args[1:]
	if len(args) == 0 {
		printUsage(os.Stderr)
		os.Exit(1)
	}
	if args[0] == "-h" || args[0] == "--help" {
		printUsage(os.Stdout)
		os.Exit(0)
	}

	var err error
	switch args[0] {
	case "acp":
		err = runACP(args[1:])
	case "sessions":
		err = runSessions(args[1:])
	case "skills":
		err = runSkills(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		printUsage(os.Stderr)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func printUsage(w *os.File) {
	fmt.Fprintf(w, `Usage:
  %[1]s -h | --help
  %[1]s -v | --version
  %[1]s acp [flags]
  %[1]s sessions list [flags]
  %[1]s skills list
  %[1]s skills install <path-or-github-or-url>
  %[1]s skills uninstall <name>
`, os.Args[0])
}

func resolveACPSessionDefaultCWD(flag string) (string, error) {
	raw := strings.TrimSpace(flag)
	if raw != "" {
		return filepath.Abs(raw)
	}
	return os.Getwd()
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func runACP(args []string) error {
	fs := flag.NewFlagSet("acp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to config.yaml (default: search ~/.config/coddy-agent/config.yaml and ./config.yaml)")
	logLevel := fs.String("log-level", "info", "debug, info, warn, error")
	acpCWD := fs.String("cwd", "", "default session working directory when the client sends an empty cwd (default: process current directory)")
	sessionsRoot := fs.String("sessions-dir", "", "sessions root directory (empty uses $HOME/coddy-agent/sessions)")
	disableSession := fs.Bool("disable-session", false, "do not write sessions to disk (in-memory only; use for cron and one-shot runs; session/load and session/list unavailable)")
	persistedSession := fs.String("session-id", "", "if snapshots exist under this id, session/new restores them once (CLI UX); otherwise a new bundle uses this folder name")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage of acp:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	defaultSessionCWD, err := resolveACPSessionDefaultCWD(*acpCWD)
	if err != nil {
		return err
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLogLevel(*logLevel),
	}))
	log.Info("starting ACP server", "version", version.Get())

	var store *session.FileStore
	if *disableSession {
		log.Info("session persistence disabled", "reason", "disable-session flag")
	} else {
		store, err = openSessionStore(*sessionsRoot)
		if err != nil {
			return err
		}
		if store != nil {
			log.Info("session persistence enabled", "root", store.Root)
		}
	}

	var srv *acp.Server
	ref := &serverRef{p: &srv}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock) (string, error) {
		agent := react.NewAgent(cfg, st, ref, log)
		return agent.Run(ctx, prompt)
	}
	mgr := session.NewManager(cfg, ref, runner, log, defaultSessionCWD, store)
	if pid := strings.TrimSpace(*persistedSession); pid != "" {
		if err := session.ValidateFolderSessionID(pid); err != nil {
			return fmt.Errorf("--session-id: %w", err)
		}
		mgr.SetPreferredSessionID(pid)
	}
	srv = acp.NewServer(mgr, log)
	mgr.SetServer(srv)

	ctx := context.Background()
	return srv.Run(ctx, os.Stdin)
}

func openSessionStore(flagValue string) (*session.FileStore, error) {
	raw := strings.TrimSpace(flagValue)
	if raw != "" {
		root, err := filepath.Abs(raw)
		if err != nil {
			return nil, fmt.Errorf("sessions-dir: %w", err)
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, fmt.Errorf("sessions-dir mkdir: %w", err)
		}
		return &session.FileStore{Root: root}, nil
	}

	root, err := session.DefaultCoddySessionsRoot()
	if err != nil {
		slog.Warn("session persistence unavailable (no HOME)", "error", err)
		return nil, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("sessions default root mkdir: %w", err)
	}
	return &session.FileStore{Root: root}, nil
}

func runSessions(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: %s sessions list [--sessions-dir <path>] [--cwd <filter>]", os.Args[0])
	}
	switch strings.TrimSpace(args[0]) {
	case "list":
		fs := flag.NewFlagSet("sessions list", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		rootFlag := fs.String("sessions-dir", "", "sessions root (empty uses default $HOME/coddy-agent/sessions)")
		cwdFilter := fs.String("cwd", "", "only list sessions saved with this cwd (absolute)")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		store, err := openSessionStore(*rootFlag)
		if err != nil {
			return err
		}
		if store == nil || store.Root == "" {
			return fmt.Errorf("session store not available")
		}
		rows, err := store.ListSnapshots(strings.TrimSpace(*cwdFilter))
		if err != nil {
			return err
		}
		fmt.Printf("%s\t%s\t%s\t%s\n", "SESSION_ID", "UPDATED_AT", "CWD", "TITLE")
		for _, r := range rows {
			title := strings.ReplaceAll(r.Title, "\t", " ")
			title = strings.ReplaceAll(title, "\n", " ")
			fmt.Printf("%s\t%s\t%s\t%s\n", r.SessionID, r.UpdatedAt, r.CWD, title)
		}
		fmt.Printf("(total %d)\n", len(rows))
		return nil
	default:
		return fmt.Errorf("unknown sessions subcommand %q (try %s sessions list)", args[0], os.Args[0])
	}
}

func runSkills(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: %s skills list|install|uninstall ...", os.Args[0])
	}
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	switch args[0] {
	case "list":
		return skills.List(cfg)
	case "install":
		if len(args) < 2 {
			return fmt.Errorf("usage: %s skills install <path-or-github-or-url>", os.Args[0])
		}
		return skills.Install(cfg, args[1])
	case "uninstall":
		if len(args) < 2 {
			return fmt.Errorf("usage: %s skills uninstall <name>", os.Args[0])
		}
		return skills.Uninstall(cfg, args[1])
	default:
		return fmt.Errorf("unknown skills subcommand %q", args[0])
	}
}
