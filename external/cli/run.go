//go:build cli

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// CommandDeps carries the cmd/coddy helpers the console surface needs.
type CommandDeps struct {
	EnsureHome func(home string) error
	OpenStore  func(flagValue string, cfg *config.Config) (*session.FileStore, error)
}

// Run parses flags, wires the manager, and drives the interactive console.
func Run(args []string, deps CommandDeps) error {
	fs := flag.NewFlagSet("cli", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to config.yaml (CODDY_CONFIG, else <home>/config.yaml)")
	homeDir := fs.String("home", "", "agent state directory (CODDY_HOME, default ~/.coddy)")
	cwdFlag := fs.String("cwd", "", "session working directory (CODDY_CWD, default process cwd)")
	sessionsRoot := fs.String("sessions-dir", "", "sessions root (empty uses config sessions.dir or ~/.coddy/sessions)")
	sessionID := fs.String("session-id", "", "reopen or create the session under this id")
	resume := fs.Bool("resume", false, "open the session picker before starting")
	var contFlag bool
	fs.BoolVar(&contFlag, "continue", false, "continue the most recent session in this folder")
	fs.BoolVar(&contFlag, "c", false, "shorthand for --continue")
	var promptFlag string
	fs.StringVar(&promptFlag, "prompt", "", "run one prompt non-interactively, print the answer, and exit")
	fs.StringVar(&promptFlag, "p", "", "shorthand for --prompt")
	modelFlag := fs.String("model", "", "select a configured model id (provider/model)")
	modeFlag := fs.String("mode", "", "start in this mode: agent|plan|ask")
	permMode := fs.String("permission-mode", "", "permission mode: ask|accept_edits|bypass")
	remoteFlag := fs.String("remote", "", "connect to a remote coddy http server (configured remote name, host:port, or http(s) URL)")
	remoteToken := fs.String("remote-token", "", "bearer token for --remote (default from CODDY_REMOTE_TOKEN)")
	themeFlag := fs.String("theme", "auto", "color theme: dark|light|auto")
	plainFlag := fs.Bool("plain", false, "deterministic rendering for tests: no terminal queries or protocol negotiation")
	logLevel := fs.String("log-level", "", "debug|info|warn|error (default from config)")
	logFile := fs.String("log-file", "", "log file path (default <home>/logs/cli.log)")
	schedulerEnabled := fs.Bool("scheduler-enabled", false, "set scheduler.enabled=true in this process (build with -tags scheduler)")
	skillsAutoDiscovery := fs.Bool(config.SkillsAutoDiscoveryFlagName, true, "model-driven skill auto-discovery (load_skill tool); pass =false to disable and override config")
	projectTrust := fs.String(config.ProjectTrustFlagName, config.ProjectTrustAsk, config.ProjectTrustFlagUsage)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage of cli (interactive console, also the default for bare %s on a terminal):\n", os.Args[0])
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	// One-shot print mode needs no terminal at all; only the interactive
	// console insists on a tty.
	printMode := strings.TrimSpace(promptFlag) != ""
	if !printMode && !IsInteractiveTerminal() {
		return errors.New("the interactive console needs a terminal on stdin and stdout (or run one prompt with -p/--prompt)")
	}

	cli := config.CLIPaths{
		Home:   strings.TrimSpace(*homeDir),
		CWD:    strings.TrimSpace(*cwdFlag),
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
	if *schedulerEnabled {
		cfg.Scheduler.Enabled = true
	}
	config.ApplySkillsAutoDiscoveryFlag(fs, cfg, skillsAutoDiscovery)
	if err := config.ApplyProjectTrustFlag(fs, cfg, projectTrust); err != nil {
		return err
	}

	ropts, err := remote.Resolve(cfg, *remoteFlag, *remoteToken)
	if err != nil {
		return err
	}

	log, logCloser, err := isolatedLogger(cfg, paths.Home, *logLevel, *logFile)
	if err != nil {
		return err
	}
	defer func() { _ = logCloser.Close() }()
	log.Info("starting interactive console", "version", version.Get())

	store, err := deps.OpenStore(*sessionsRoot, cfg)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Flag conflicts shared by both modes.
	if *resume && strings.TrimSpace(*sessionID) != "" {
		return errors.New("--resume and --session-id are mutually exclusive")
	}
	if contFlag && strings.TrimSpace(*sessionID) != "" {
		return errors.New("--continue and --session-id are mutually exclusive")
	}
	if contFlag && *resume {
		return errors.New("--continue and --resume are mutually exclusive")
	}
	opts := startupOptions{
		model:    strings.TrimSpace(*modelFlag),
		mode:     strings.TrimSpace(*modeFlag),
		permMode: strings.TrimSpace(*permMode),
	}

	if printMode {
		if *resume {
			return errors.New("--resume needs the interactive picker; use --continue or --session-id with --prompt")
		}
		popts := PrintOptions{
			Prompt:       promptFlag,
			Out:          os.Stdout,
			ErrOut:       os.Stderr,
			SessionID:    strings.TrimSpace(*sessionID),
			ContinueLast: contFlag,
			Model:        opts.model,
			Mode:         opts.mode,
			PermMode:     opts.permMode,
			Config:       cfg,
		}
		if ropts != nil {
			ropts.Log = log
			warnInsecureRemote(ropts, os.Stderr)
			h, herr := remote.NewHandler(*ropts)
			if herr != nil {
				return herr
			}
			h.SetServer(&printSender{mgr: h, cfg: cfg, out: os.Stdout, errOut: os.Stderr, remote: true})
			return PrintPrompt(ctx, h, popts)
		}
		lateSender := &lateBoundSender{}
		var mgr *session.Manager
		runner := func(rctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
			return newTurnAgent(mgr, nil, st, snd, log).Run(rctx, prompt)
		}
		mgr = session.NewManager(cfg, lateSender, runner, log, cfg.Paths.CWD, store)
		lateSender.inner = &printSender{mgr: mgr, cfg: cfg, out: os.Stdout, errOut: os.Stderr}
		return PrintPrompt(ctx, mgr, popts)
	}

	term := tui.NewProcessTerminal()
	term.SetPlain(*plainFlag)

	var app *App
	if ropts != nil {
		ropts.Log = log
		warnInsecureRemote(ropts, os.Stderr)
		app, err = buildRemoteApp(cfg, ropts, log, term, resolveThemeName(*themeFlag, *plainFlag), *plainFlag)
		if err != nil {
			return err
		}
	} else {
		app = buildApp(cfg, store, log, term, resolveThemeName(*themeFlag, *plainFlag), *plainFlag)
	}
	llmWarmupNotices(log, cfg)

	// Startup that can fail happens before raw mode. With --resume no session
	// is created here: the picker inside the interactive loop decides first
	// and the startup options apply to whichever session it picks.
	if !*resume {
		var startErr error
		if contFlag {
			startErr = app.StartContinue(ctx)
		} else {
			startErr = app.Start(ctx, strings.TrimSpace(*sessionID), false)
		}
		if startErr != nil {
			return startErr
		}
		if err := app.ApplyStartupOptions(ctx, opts.model, opts.mode, opts.permMode); err != nil {
			return err
		}
	}

	if err := runInteractive(ctx, app, term, *resume, opts); err != nil {
		return err
	}
	// The resume hint prints after the terminal is fully restored, so it
	// lands in normal scrollback under the finished transcript.
	if hint := app.ExitHint(); hint != "" {
		fmt.Println(hint)
	}
	return nil
}

// buildApp wires the manager triple (store -> manager -> app) with the app's
// sender registered as the manager's server sender, so replay, mode, config,
// and slash-catalog updates reach the console. The runner only executes
// inside manager prompt handling, so mgr and app are set by the time it runs.
func buildApp(cfg *config.Config, store *session.FileStore, log *slog.Logger, term appTerminal, themeName string, plain bool) *App {
	var app *App
	var mgr *session.Manager
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return newTurnAgent(mgr, app, st, snd, log).Run(ctx, prompt)
	}
	lateSender := &lateBoundSender{}
	mgr = session.NewManager(cfg, lateSender, runner, log, cfg.Paths.CWD, store)
	app = newApp(cfg, mgr, log, term, themeName, plain)
	lateSender.inner = app.Sender()
	return app
}

// newTurnAgent builds the ReAct agent for one local console turn on the
// manager's live configuration and wires the staged config flow to the
// manager's runtime reload, as cmd/coddy does for ACP and external/httpserver
// for HTTP. Without the reloader the agent hides config_set through
// config_rollback and the model can only read Coddy's configuration. After a
// successful reload the app (nil in print mode) adopts the new config so its
// model catalog, footer, and header follow the file.
func newTurnAgent(mgr *session.Manager, app *App, st *session.State, snd acp.UpdateSender, log *slog.Logger) *agent.Agent {
	loop := agent.NewAgent(mgr.Cfg(), st, snd, log)
	loop.SetConfigReloader(func(ctx context.Context) ([]string, error) {
		warnings, err := mgr.ReloadConfigForSession(ctx, st)
		if err == nil && app != nil {
			app.replaceConfig(mgr.Cfg(), st)
		}
		return warnings, err
	})
	return loop
}

// warnInsecureRemote flags a bearer token about to travel over plain http to
// another machine. The connection still proceeds: LAN use is the main remote
// scenario, but the operator should know the token is readable on the wire.
func warnInsecureRemote(ropts *remote.Options, w io.Writer) {
	if ropts == nil || !ropts.Insecure || ropts.Token == "" {
		return
	}
	_, _ = fmt.Fprintf(w, "warning: sending the bearer token over plain http to %s; prefer https or a trusted network\n", ropts.BaseURL)
}

// buildRemoteApp wires the console against a remote coddy http server: the
// remote handler replaces the manager and streams ACP updates back through
// the app's sender.
func buildRemoteApp(cfg *config.Config, ropts *remote.Options, log *slog.Logger, term appTerminal, themeName string, plain bool) (*App, error) {
	h, err := remote.NewHandler(*ropts)
	if err != nil {
		return nil, err
	}
	lateSender := &lateBoundSender{}
	h.SetServer(lateSender)
	app := newApp(cfg, h, log, term, themeName, plain)
	app.remoteURL = h.BaseURL()
	lateSender.inner = app.Sender()
	return app, nil
}

// lateBoundSender lets the manager be constructed before the app exists.
type lateBoundSender struct{ inner acp.UpdateSender }

func (l *lateBoundSender) SendSessionUpdate(sessionID string, update interface{}) error {
	if l.inner == nil {
		return nil
	}
	return l.inner.SendSessionUpdate(sessionID, update)
}

func (l *lateBoundSender) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	if l.inner == nil {
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	}
	return l.inner.RequestPermission(ctx, params)
}

func (l *lateBoundSender) RequestQuestion(ctx context.Context, params acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	if l.inner == nil {
		return &acp.QuestionResult{}, nil
	}
	return l.inner.RequestQuestion(ctx, params)
}

// llmWarmupNotices logs provider auth notices without touching the terminal.
func llmWarmupNotices(log *slog.Logger, cfg *config.Config) {
	_ = log
	_ = cfg
}

// startupOptions carries --model/--mode/--permission-mode into the resume path.
type startupOptions struct {
	model    string
	mode     string
	permMode string
}

// runInteractive owns the raw-mode lifecycle: every exit path restores the
// terminal (normal quit, error, panic, signal).
func runInteractive(ctx context.Context, app *App, term *tui.ProcessTerminal, resume bool, opts startupOptions) (err error) {
	buf := tui.NewStdinBuffer(tui.EscTimeout())
	buf.Emit = app.OnTerminalInput
	buf.EmitPaste = app.OnTerminalPaste

	if startErr := term.Start(func(data []byte) { buf.Feed(data) }, app.OnTerminalResize); startErr != nil {
		return fmt.Errorf("terminal: %w", startErr)
	}
	term.HideCursor()

	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		if app.turnActive && app.sessionID != "" {
			app.mgr.HandleSessionCancel(acp.SessionCancelParams{SessionID: app.sessionID})
		}
		app.Close()
		app.JoinWorkers(3 * time.Second)
		app.stopSpinner()
		app.screen.FinishInline()
		app.screen.Stop()
		term.Stop()
	}
	defer func() {
		if r := recover(); r != nil {
			restore()
			panic(r)
		}
		restore()
	}()

	if resume {
		if err := app.Start(ctx, "", true); err != nil {
			return err
		}
		if err := app.ApplyStartupOptions(ctx, opts.model, opts.mode, opts.permMode); err != nil {
			return err
		}
		app.populateHeader()
	}

	return app.Run(ctx)
}

// isolatedLogger forces log output away from the terminal: exactly one file
// sink, never stdout/stderr, regardless of YAML configuration.
func isolatedLogger(cfg *config.Config, home, level, file string) (*slog.Logger, io.Closer, error) {
	path := strings.TrimSpace(file)
	if path == "" {
		path = filepath.Join(home, "logs", "cli.log")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, fmt.Errorf("log dir: %w", err)
	}
	cfg.Logger.ApplyOverrides(config.LoggerCLIOverrides{
		Level:  strings.TrimSpace(level),
		Output: "file",
		File:   path,
	})
	cfg.Logger.Outputs = []string{"file"}
	cfg.Logger.File = path
	log, closer, err := logger.New(cfg.Logger)
	if err != nil {
		return nil, nil, fmt.Errorf("log: %w", err)
	}
	return log, closer, nil
}

// resolveThemeName picks the concrete theme for auto mode. The full OSC 11
// probe is layered on later; the deterministic fallback is COLORFGBG then
// dark (plain mode always resolves immediately).
func resolveThemeName(flagValue string, plain bool) string {
	switch flagValue {
	case "dark", "light":
		return flagValue
	}
	if v := os.Getenv("COLORFGBG"); v != "" {
		parts := strings.Split(v, ";")
		if len(parts) >= 2 {
			bg := parts[len(parts)-1]
			if bg == "7" || bg == "15" {
				return "light"
			}
		}
	}
	_ = plain
	return "dark"
}

// IsInteractiveTerminal reports whether stdin and stdout are both terminals.
func IsInteractiveTerminal() bool { return tui.IsTerminal() }

var _ = logger.New
