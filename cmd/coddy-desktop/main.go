//go:build desktop && http

// Command coddy-desktop opens Coddy as a window on this machine.
//
// It is a binary of its own rather than a verb of `coddy`, because what it
// needs is a different shape of program: `coddy serve` is a server that
// outlives every client, and this is a client whose server lives exactly as
// long as the window. Keeping them apart also keeps the desktop surface out of
// the binary that runs on servers and in containers, where there is no display
// for a window to open on.
//
// The window is the operator's Chromium-family browser in app mode, in a
// profile of its own. The native window around the system WebView arrives with
// the desktop.native tag, which needs cgo.
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

	"github.com/EvilFreelancer/coddy-agent/external/desktop"
	"github.com/EvilFreelancer/coddy-agent/external/httpserver"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// hiddenSections are the settings sections the window does not offer.
//
// They are the surfaces of `coddy serve` - a bot polling a messenger, a relay,
// a cron daemon, an exporter - and the desktop turns every one of them off
// before it starts (desktop.ApplyConfig). Offering a switch the window ignores
// would be worse than not offering it, so the form of this binary does not
// draw them whatever tags it was built with.
var hiddenSections = []string{"gateways", "swarm", "scheduler", "metrics", "browser"}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		fmt.Fprintf(os.Stderr, "coddy-desktop: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("coddy-desktop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to config.yaml (CODDY_CONFIG, else <home>/config.yaml)")
	homeDir := fs.String("home", "", "agent state directory (CODDY_HOME, default ~/.coddy)")
	cwd := fs.String("cwd", "", "default workspace for new chats (CODDY_CWD, default process cwd)")
	window := fs.String("window", desktop.WindowAuto, "window backend: auto|browser|native")
	browser := fs.String("browser", "", "browser to open the window with, by name on PATH or by path (else "+desktop.BrowserEnvVar+", else the first Chromium-family browser installed)")
	logLevel := fs.String("log-level", "", "log level: debug|info|warn|error (default from config)")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.BoolVar(showVersion, "v", false, "alias of --version")
	framed := fs.Bool("frame", false, "keep the system title bar and borders. Without it the window is a bare rectangle and the tab strip is its title bar: it carries the window's own buttons and is what you drag it by (X11 sessions; elsewhere the frame stays)")
	keepAuth := fs.Bool("auth", false, "ask for the credential the configuration carries (a token, the sign-in form). By default the window asks for nothing: it binds loopback for the person who started it, and a form in a window they opened themselves proves nothing")
	testConfig := config.AddCheckFlag(fs)

	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage of coddy-desktop (opens Coddy in a window on this machine):\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unknown argument %q: coddy-desktop takes flags only", fs.Arg(0))
	}
	if *showVersion {
		fmt.Println(version.Get())
		return nil
	}

	cli := config.CLIPaths{
		Home:   strings.TrimSpace(*homeDir),
		CWD:    strings.TrimSpace(*cwd),
		Config: strings.TrimSpace(*cfgPath),
	}
	// A config check reports on the file and leaves: no home layout is
	// created, no port is taken and no window opens.
	if *testConfig {
		return config.RunCheck(os.Stdout, cli)
	}

	paths, err := config.Resolve(cli)
	if err != nil {
		return err
	}
	if err := serve.EnsureHomeLayout(paths.Home); err != nil {
		return err
	}
	cfg, err := config.LoadFromCLI(cli)
	if err != nil {
		return err
	}
	_, _ = skills.SeedDelivery(cfg)

	// The window runs the web interface and nothing else, and it says so once.
	// Nothing in the configuration is changed to achieve that: the surface it
	// runs is the one subsystem descriptor serveHTTP hands the supervisor, and
	// the document stays exactly as the operator wrote it - which is what the
	// settings screen inside the window reads, and therefore what a save from
	// that screen writes back.
	if left := desktop.SurfacesLeftToServe(cfg); len(left) > 0 {
		fmt.Fprintf(os.Stderr, "coddy-desktop runs the web interface only; left to `coddy serve`: %s\n", strings.Join(left, ", "))
	}
	if lvl := strings.TrimSpace(*logLevel); lvl != "" {
		cfg.Logger.ApplyOverrides(config.LoggerCLIOverrides{Level: lvl})
	}
	log, logCloser, err := logger.New(cfg.Logger)
	if err != nil {
		return fmt.Errorf("log: %w", err)
	}
	defer func() { _ = logCloser.Close() }()

	rt := &serve.Runtime{}
	if err := rt.Init(serve.Options{
		CLI:           cli,
		NeedsSessions: true,
		Log:           log,
		Cfg:           cfg,
	}); err != nil {
		return err
	}
	log.Info("starting coddy-desktop", "version", version.Get(), "config", paths.ConfigPath, "workspace", paths.CWD)
	llm.SetACPLogger(log)
	defer llm.StopACPAgents()
	if rt.Mgr != nil {
		rt.Mgr.StartGlobalMCPServers()
		defer rt.Mgr.CloseMCP()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The handle is given to the server before there is a window, because the
	// page is served the moment the surface is up and the browser window
	// appears a second later; the launcher attaches the window to it then.
	windowHandle := desktop.NewWindowHandle()
	return desktop.Run(ctx, desktop.Options{
		Home:    paths.Home,
		Window:  strings.TrimSpace(*window),
		Browser: desktop.BrowserCommand{Path: strings.TrimSpace(*browser)},
		Log:     log,
		Handle:  windowHandle,
		Framed:  *framed,
		Serve:   serveHTTP(rt, paths.Home, !*keepAuth, windowHandle),
	})
}

// serveHTTP is the surface behind the window: the HTTP server, and nothing
// else, on the address the launcher picked.
//
// It goes through the supervisor rather than calling httpserver.Serve
// directly, so that a configuration saved from the window's own settings
// screen reaches the server the same way it does under `coddy serve`. The
// address is the launcher's and never the file's, so the one change a running
// server cannot adopt - a listen address that moved - cannot happen here.
func serveHTTP(rt *serve.Runtime, home string, noAuth bool, window *desktop.WindowHandle) func(context.Context, string) error {
	return func(ctx context.Context, addr string) error {
		at := func(*config.Config) string { return addr }
		all := []serve.Subsystem{{
			Kind:          serve.KindHTTP,
			ConfigKey:     "httpserver.enable",
			BuildTag:      "http",
			Available:     httpserver.Available,
			NeedsSessions: true,
			// The window is this surface, so the key that decides whether a
			// server runs it does not speak for it: `httpserver.enable:
			// false`, set for a node that only polls a messenger, must not
			// leave a desktop with nothing to open on. Every other subsystem
			// is absent from this list, which is why none of them runs and why
			// nothing had to be turned off in the operator's configuration.
			Enabled:    func(*config.Config) bool { return true },
			Address:    at,
			RestartKey: at,
			Run: func(ctx context.Context, cfg *config.Config) error {
				withdrawPrompts := func() {}
				return httpserver.Serve(ctx, httpserver.Options{
					Cfg: cfg, Mgr: rt.Mgr, Log: rt.Log,
					DefaultCWD: rt.Paths.CWD, Home: home,
					ListenAddr: addr, DetachedPrompts: rt, Wakes: rt,
					NoAuth:          noAuth,
					DesktopWindow:   window,
					UnbuiltSections: hiddenSections, Status: rt.StatusDocument,
					OnServer: func(s *httpserver.Server) {
						if s == nil {
							rt.SetTurnMirror(nil)
							withdrawPrompts()
							return
						}
						rt.SetTurnMirror(s)
						withdrawPrompts = rt.AddDetachedPermissionApprover(s)
					},
				})
			},
		}}
		cfg := rt.Cfg()
		if _, err := serve.Resolve(cfg, all); err != nil {
			return err
		}
		reloads := make(chan *config.Config, 1)
		removeObserver := rt.AddConfigObserver(func(next *config.Config) { offerNewest(reloads, next) })
		defer removeObserver()

		sup := serve.NewSupervisor(rt.Log, all)
		rt.SetSupervisor(sup)
		// Nothing supervises a window: the operator is the only one who would
		// start it again, so a change this process cannot adopt is a warning
		// in the log, not an exit.
		sup.Restartable = false
		return sup.Run(ctx, cfg, reloads)
	}
}

// offerNewest keeps only the newest configuration in the supervisor's queue.
// An observer must not block the goroutine that replaced it, and a document
// already superseded is wrong by the time the supervisor would read it.
func offerNewest(ch chan *config.Config, next *config.Config) {
	for {
		select {
		case ch <- next:
			return
		default:
		}
		select {
		case <-ch:
		default:
			return
		}
	}
}
