// Command coddy is the Coddy Agent CLI (ACP server and skills).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// serverRef breaks the cyclic dependency between acp.Server and session.Manager.
type serverRef struct {
	p    **acp.Server
	cfg  *config.Config
	live func() *config.Config // optional; when set, overrides cfg for permission checks (HTTP hot reload)
}

func (r *serverRef) liveCfg() *config.Config {
	if r.live != nil {
		if c := r.live(); c != nil {
			return c
		}
	}
	return r.cfg
}

func (r *serverRef) SendSessionUpdate(sessionID string, update interface{}) error {
	s := *r.p
	if s == nil {
		return nil
	}
	return s.SendSessionUpdate(sessionID, update)
}

func (r *serverRef) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	if cfg := r.liveCfg(); cfg != nil && cfg.Tools.ResolvedPermMode() == config.PermModeBypass {
		return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
	}
	s := *r.p
	if s == nil {
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	}
	return s.RequestPermission(ctx, params)
}

func (r *serverRef) RequestQuestion(ctx context.Context, params acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	s := *r.p
	if s == nil {
		return &acp.QuestionResult{}, nil
	}
	return s.RequestQuestion(ctx, params)
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
	case "http":
		err = runHTTP(args[1:])
	case "gateway":
		err = runGateway(args[1:])
	case "sessions":
		err = runSessions(args[1:])
	case "skills":
		err = runSkills(args[1:])
	case "plugin":
		err = runPlugin(args[1:])
	case "rules":
		err = runRules(args[1:])
	case "update":
		err = runUpdate(args[1:])
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
	_, _ = fmt.Fprintf(w, `Usage:
  %[1]s -h | --help
  %[1]s -v | --version
  %[1]s acp [flags] (Agent Client Protocol)
  %[1]s http [flags] (OpenAI-compatible HTTP)
  %[1]s gateway [flags] (messenger gateway: Telegram etc.)
  %[1]s sessions list [flags]
  %[1]s skills list
  %[1]s skills enable <name>
  %[1]s skills disable <name>
  %[1]s skills add <owner/repo | git-url | marketplace-url>
  %[1]s skills sync
  %[1]s skills remove <name>
  %[1]s plugin marketplace list | add <src> | remove <src> | sync
  %[1]s plugin install <owner/repo | git-url | marketplace-url>
  %[1]s plugin remove <name>
  %[1]s plugin enable <name> | disable <name>
  %[1]s rules list [--cwd DIR]
  %[1]s update [flags]
`, os.Args[0])
}

func runACP(args []string) error {
	fs := flag.NewFlagSet("acp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to config.yaml (CODDY_CONFIG, else <home>/config.yaml or legacy search paths)")
	logLevel := fs.String("log-level", "", "debug|info|warn|error (default from config)")
	logOutput := fs.String("log-output", "", "stdout|stderr|file|both (default from config)")
	logFile := fs.String("log-file", "", "log file path when output includes file (default from config)")
	logFormat := fs.String("log-format", "", "text|json (default from config)")
	homeDir := fs.String("home", "", "agent state directory (CODDY_HOME, default ~/.coddy)")
	acpCWD := fs.String("cwd", "", "default session cwd when the client sends an empty cwd (CODDY_CWD, default process cwd)")
	sessionsRoot := fs.String("sessions-dir", "", "sessions root (empty uses config sessions.dir or ~/.coddy/sessions)")
	persistedSession := fs.String("session-id", "", "if snapshots exist under this id, session/new restores them once (CLI UX); otherwise a new bundle uses this folder name")
	schedulerEnabled := fs.Bool("scheduler-enabled", false, "set scheduler.enabled=true in this process (build with -tags scheduler)")
	skillsAutoDiscovery := fs.Bool(config.SkillsAutoDiscoveryFlagName, true, "model-driven skill auto-discovery (load_skill tool); pass =false to disable and override config")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage of acp:\n")
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
		CWD:    strings.TrimSpace(*acpCWD),
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

	log.Info("starting ACP server", "version", version.Get())

	if cfg.SchedulerEffectiveEnabled() {
		scheduler.Start(context.Background(), cfg, log, paths.CWD)
	}

	store, err := openSessionStore(*sessionsRoot, cfg)
	if err != nil {
		return err
	}
	log.Info("session persistence enabled", "root", store.Root)

	var srv *acp.Server
	ref := &serverRef{p: &srv, cfg: cfg}
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
	srv = acp.NewServer(mgr, log)
	mgr.SetServer(srv)

	ctx := context.Background()
	return srv.Run(ctx, os.Stdin)
}

func ensureCoddyHomeLayout(home string) error {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	for _, name := range []string{"sessions", "skills", "scheduler"} {
		p := filepath.Join(home, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", p, err)
		}
	}
	return nil
}

func openSessionStore(flagValue string, cfg *config.Config) (*session.FileStore, error) {
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

	root := cfg.ResolvedSessionsRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("sessions root mkdir: %w", err)
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
		rootFlag := fs.String("sessions-dir", "", "sessions root (empty uses config sessions.dir or ~/.coddy/sessions)")
		cwdFilter := fs.String("cwd", "", "only list sessions saved with this cwd (absolute)")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		cfg, err := config.LoadFromCLI(config.CLIPaths{})
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		store, err := openSessionStore(*rootFlag, cfg)
		if err != nil {
			return err
		}
		if store == nil || store.Root == "" {
			return fmt.Errorf("session store not available")
		}
		rows, err := store.ListSnapshots(strings.TrimSpace(*cwdFilter), false)
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
		return fmt.Errorf("usage: %s skills list|enable|disable|add|sync|remove", os.Args[0])
	}
	cfg, err := config.LoadFromCLI(config.CLIPaths{})
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	switch args[0] {
	case "list":
		return skills.List(cfg)
	case "enable":
		if len(args) < 2 {
			return fmt.Errorf("usage: %s skills enable <name>", os.Args[0])
		}
		if err := skills.Enable(cfg, args[1]); err != nil {
			return err
		}
		fmt.Printf("Enabled skill %q\n", args[1])
		return nil
	case "disable":
		if len(args) < 2 {
			return fmt.Errorf("usage: %s skills disable <name>", os.Args[0])
		}
		if err := skills.Disable(cfg, args[1]); err != nil {
			return err
		}
		fmt.Printf("Disabled skill %q\n", args[1])
		return nil
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("usage: %s skills add <owner/repo | git-url | marketplace-url>", os.Args[0])
		}
		added, err := skills.AddSource(cfg, args[1])
		if err != nil {
			return err
		}
		if added {
			fmt.Printf("Added skill source %q. Run `%s skills sync` to install.\n", args[1], os.Args[0])
		} else {
			fmt.Printf("Source %q already configured.\n", args[1])
		}
		return nil
	case "sync":
		res, err := skills.Sync(context.Background(), cfg)
		if err != nil {
			return err
		}
		printSyncResult(res)
		return nil
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: %s skills remove <name>", os.Args[0])
		}
		if err := skills.RemoveRemote(cfg, args[1]); err != nil {
			return err
		}
		fmt.Printf("Removed remote skill %q\n", args[1])
		return nil
	default:
		return fmt.Errorf("unknown skills subcommand %q", args[0])
	}
}

// runPlugin implements `coddy plugin ...` — the Claude-Code-style plugin and
// marketplace surface, sharing skills.RunPluginCommand with the chat /plugin
// command so both stay in lockstep.
func runPlugin(args []string) error {
	cfg, err := config.LoadFromCLI(config.CLIPaths{})
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cwd, _ := os.Getwd()
	out, err := skills.RunPluginCommand(context.Background(), cfg, cwd, args)
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

func printSyncResult(res *skills.SyncResult) {
	fmt.Printf("Synced: %d added, %d updated, %d failed.\n", len(res.Added), len(res.Updated), len(res.Failed))
	for _, n := range res.Added {
		fmt.Printf("  + %s\n", n)
	}
	for _, n := range res.Updated {
		fmt.Printf("  ~ %s\n", n)
	}
	for _, f := range res.Failed {
		fmt.Printf("  ! %s: %s\n", f.Source, f.Error)
	}
}

func runRules(args []string) error {
	cfg, err := config.LoadFromCLI(config.CLIPaths{})
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cwd := "."
	if len(args) >= 1 && args[0] == "list" {
		if len(args) >= 3 && args[1] == "--cwd" {
			cwd = args[2]
		}
		return rules.ListCatalog(cwd, rules.DefaultFactory(), rules.ParseSystems(cfg.Rules.Systems))
	}
	return fmt.Errorf("usage: %s rules list [--cwd DIR]", os.Args[0])
}
