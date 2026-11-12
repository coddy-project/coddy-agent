//go:build desktop && http && ui

// `coddy desktop` opens Coddy as a window on this machine.
//
// The window uses Cocoa/WKWebView on macOS with desktop.native and cgo, or
// a Chromium-family browser in app mode with its own profile, over a loopback
// HTTP surface whose port the agent home remembers. The page is a tab strip
// with one chat per tab (external/desktop/shell). The whole of Coddy is behind it - the same
// binary, the same configuration, the same session store - so a window is
// another way in, not a smaller Coddy.
//
// The ui tag is in the constraint beside http because the window opens on the
// SPA: a binary with the server but no interface would start a browser on a
// page that is not there.
//
// It is a verb of `coddy` rather than a binary of its own because the surface
// costs 0.44 MiB in a binary that already carries the HTTP server and the SPA:
// the X11 library it drives the window with (jezek/xgb) is already there for
// the console's clipboard pictures, and only the DBus client of the tray icon
// is new. A second 72 MiB artefact to ship, package and update was the larger
// cost by far.
//
// The macOS native window needs desktop.native, cgo and macOS 14 or later.
// Release builds use the browser backend.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/EvilFreelancer/coddy-agent/external/desktop"
	"github.com/EvilFreelancer/coddy-agent/external/httpserver"
	"github.com/EvilFreelancer/coddy-agent/external/swarm"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/localipc"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// runDesktop is the `desktop` command: it returns when the window closes.
//
// A context that was cancelled - Ctrl+C, or Quit in the tray's menu - is the
// ordinary end of a window and not a failure, so it is swallowed here rather
// than printed by main as an error.
func runDesktop(args []string) error {
	if err := runDesktopWindow(args); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func runDesktopWindow(args []string) error {
	fs := flag.NewFlagSet("coddy desktop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to config.yaml (CODDY_CONFIG, else <home>/config.yaml)")
	homeDir := fs.String("home", "", "agent state directory (CODDY_HOME, default ~/.coddy)")
	cwd := fs.String("cwd", "", "default workspace for new chats (CODDY_CWD, default process cwd)")
	window := fs.String("window", desktop.WindowAuto, "window backend: auto|browser|native")
	browser := fs.String("browser", "", "browser to open the window with, by name on PATH or by path (else "+desktop.BrowserEnvVar+", else the first Chromium-family browser installed)")
	logLevel := fs.String("log-level", "", "log level: debug|info|warn|error (default from config)")
	newChat := fs.Bool("new-chat", false, "open a new chat in --cwd. A desktop already running is asked for the chat rather than a second one being started, which is what a row of the application menu naming a recent workspace does")
	framed := fs.Bool("frame", false, "keep the system title bar and borders. Without it the window is a bare rectangle and the tab strip is its title bar: it carries the window's own buttons and is what you drag it by (X11 or native macOS; elsewhere the frame stays)")
	keepAuth := fs.Bool("auth", false, "ask for the credential the configuration carries (a token, the sign-in form). By default the window asks for nothing: it binds loopback for the person who started it, and a form in a window they opened themselves proves nothing")
	testConfig := config.AddCheckFlag(fs)

	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage of coddy desktop (opens Coddy in a window on this machine):\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unknown argument %q: coddy desktop takes flags only", fs.Arg(0))
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

	// The window is one surface of this process; beside it the desktop runs
	// whatever the configuration asks of `coddy serve` - the public HTTP
	// surface a phone reaches on the configured address, a gateway, a relay,
	// the scheduler - through the same supervisor and the same file watching.
	// Nothing in the configuration is changed to get there: a save from the
	// settings screen inside the window writes back exactly what the operator
	// had. What the launch itself decided is re-applied on every reload.
	adjust := desktopConfigAdjust(strings.TrimSpace(*logLevel))
	if err := adjust(cfg); err != nil {
		// The window is what this process exists for; a surface the
		// configuration names that cannot come up says so in the log rather
		// than keeping the window shut over it.
		fmt.Fprintf(os.Stderr, "coddy desktop: %v\n", err)
	}
	log, logCloser, err := logger.New(cfg.Logger)
	if err != nil {
		return fmt.Errorf("log: %w", err)
	}
	defer func() { _ = logCloser.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A running serve owns this home. Attach before constructing anything
	// stateful: no second manager, MCP pool, metrics registry, gateway,
	// scheduler or config watcher is created in this mode.
	if shared, sharedErr := connectDesktopRuntime(ctx, paths.Home); sharedErr == nil {
		log.Info("attaching the desktop window to the running Coddy service", "config", paths.ConfigPath, "workspace", paths.CWD)
		windowHandle := desktop.NewWindowHandle()
		return desktop.Run(ctx, desktop.Options{
			Home: paths.Home, Window: strings.TrimSpace(*window),
			Browser: desktop.BrowserCommand{Path: strings.TrimSpace(*browser)},
			Log:     log, Handle: windowHandle, Framed: *framed, NewChat: *newChat, CWD: paths.CWD,
			Serve: serveAttachedDesktop(shared, !*keepAuth, windowHandle),
		})
	} else if !errors.Is(sharedErr, localipc.ErrUnavailable) {
		return sharedErr
	}

	// Nobody answered the authenticated IPC probe. Claim the home before the
	// manager is built; if a service won the race, attach to it instead.
	runtimeOwner, err := claimRuntimeOwner(paths.Home, true)
	if err != nil {
		if errors.Is(err, localipc.ErrAlreadyOwned) {
			shared, sharedErr := connectDesktopRuntime(ctx, paths.Home)
			if sharedErr != nil {
				return sharedErr
			}
			windowHandle := desktop.NewWindowHandle()
			return desktop.Run(ctx, desktop.Options{
				Home: paths.Home, Window: strings.TrimSpace(*window),
				Browser: desktop.BrowserCommand{Path: strings.TrimSpace(*browser)},
				Log:     log, Handle: windowHandle, Framed: *framed, NewChat: *newChat, CWD: paths.CWD,
				Serve: serveAttachedDesktop(shared, !*keepAuth, windowHandle),
			})
		}
		return err
	}
	defer func() {
		if err := runtimeOwner.Close(); err != nil {
			log.Warn("closing the local runtime IPC", "error", err)
		}
	}()

	rt := &serve.Runtime{}
	if err := rt.Init(serve.Options{
		CLI:           cli,
		NeedsSessions: true,
		Log:           log,
		Cfg:           cfg,
	}); err != nil {
		return err
	}
	wiring := newMetricsWiring(rt)
	// The collectors count for the whole process, whether or not the metrics
	// listener is enabled: the desktop starts them the way coddy serve does,
	// or a scrape answers every Coddy family empty.
	defer wiring.Start()()
	log.Info("starting the desktop window", "version", version.Get(), "config", paths.ConfigPath, "workspace", paths.CWD)
	llm.SetACPLogger(log)
	defer llm.StopACPAgents()
	if rt.Mgr != nil {
		rt.Mgr.StartGlobalMCPServers()
		defer rt.Mgr.CloseMCP()
	}

	// The handle is given to the server before there is a window, because the
	// page is served the moment the surface is up and the browser window
	// appears a second later; the launcher attaches the window to it then.
	windowHandle := desktop.NewWindowHandle()
	return desktop.Run(ctx, desktop.Options{
		Home:          paths.Home,
		Window:        strings.TrimSpace(*window),
		Browser:       desktop.BrowserCommand{Path: strings.TrimSpace(*browser)},
		Log:           log,
		Handle:        windowHandle,
		Framed:        *framed,
		NewChat:       *newChat,
		CWD:           paths.CWD,
		RecentFolders: recentWorkspaces(rt, log),
		Serve:         serveHTTPWithOwner(rt, paths.Home, !*keepAuth, windowHandle, wiring, strings.TrimSpace(*logLevel), runtimeOwner),
	})
}

// recentWorkspaces are the folders this home's chats were last had in, most
// recent first: the rows of the application entry's menu, where picking one
// opens a new chat in that folder.
//
// They are read here rather than in the launcher because the session store is
// the agent's and the launcher knows nothing of it. A store that cannot be
// read is a menu with no folders in it, which is what the entry looked like
// before there were any.
func recentWorkspaces(rt *serve.Runtime, log *slog.Logger) []string {
	if rt == nil || rt.Mgr == nil {
		return nil
	}
	store := rt.Mgr.FileStore()
	if store == nil {
		return nil
	}
	rows, err := store.ListSnapshots("", false)
	if err != nil {
		log.Debug("the recent workspaces could not be read", "error", err)
		return nil
	}
	// Newest first, by the time the session was last written.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].UpdatedAt > rows[j].UpdatedAt })
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.CWD) != "" {
			out = append(out, row.CWD)
		}
	}
	return out
}

// serveHTTP is the serving half of `coddy desktop`: the same site `coddy
// serve` would run - every subsystem the configuration enables, on the
// configured addresses - beside the window's own loopback surface, so the web
// UI a phone opens on this machine's address is the one the window drives.
//
// It goes through the supervisor rather than calling httpserver.Serve
// directly, so that a configuration saved from the window's own settings
// screen reaches every surface the same way it does under `coddy serve`.
// Every configured surface is auxiliary here: the window is what this
// process exists for, and a surface that cannot come up - a port already
// bound by a `coddy serve` of the same home, a token a gateway refuses -
// stops alone instead of taking the window down with it.
func serveHTTP(rt *serve.Runtime, home string, noAuth bool, window *desktop.WindowHandle, wiring metricsWiring, logLevel string) func(context.Context, string) error {
	return serveHTTPWithOwner(rt, home, noAuth, window, wiring, logLevel, nil)
}

func serveHTTPWithOwner(rt *serve.Runtime, home string, noAuth bool, window *desktop.WindowHandle, wiring metricsWiring, logLevel string, runtimeOwner *localipc.Owner) func(context.Context, string) error {
	return func(ctx context.Context, addr string) error {
		deps := subsystemDeps{
			httpListenAddr: func(c *config.Config) string {
				return net.JoinHostPort(c.HTTPServer.DefaultListenHost(), c.HTTPServer.DefaultListenPortString())
			},
			swarmListenAddr: func(c *config.Config) string {
				return net.JoinHostPort(c.Swarm.Host, strconv.Itoa(c.Swarm.EffectivePort()))
			},
			home:            home,
			httpAuthTokens:  outOfBandTokens("", httpserver.TokenEnvVar),
			httpLogin:       outOfBandLogin(),
			swarmAuthTokens: outOfBandTokens("", swarm.TokenEnvVar),
			adjust:          desktopConfigAdjust(logLevel),
			metrics:         wiring,
		}
		all := desktopSubsystems(rt, deps, addr, noAuth, window)
		all = runtimeIPCSubsystems(all, rt, deps, runtimeOwner)
		cfg := rt.Cfg()
		warnDesktopSubsystems(rt.Log, cfg, all)
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

// warnDesktopSubsystems reports a surface the configuration enables but this
// binary cannot run. Unlike coddy serve's pre-flight, the desktop must not
// refuse to open its window over an optional surface; the supervisor skips
// the unavailable one and this warning says what is missing and how to add it.
func warnDesktopSubsystems(log *slog.Logger, cfg *config.Config, all []serve.Subsystem) {
	for _, sub := range all {
		if sub.Available || sub.Enabled == nil || !sub.Enabled(cfg) {
			continue
		}
		log.Warn("enabled subsystem is not built in; the desktop window will run without it",
			"subsystem", string(sub.Kind), "key", sub.ConfigKey,
			"hint", "rebuild with -tags "+sub.BuildTag)
	}
}

// desktopSubsystems is the surface list of `coddy desktop`: the window's
// loopback surface first - it is what this process exists for, so it comes up
// first and stops last - then every surface the configuration enables, built
// by the same `subsystems` `coddy serve` uses and marked auxiliary so each
// fails on its own.
func desktopSubsystems(rt *serve.Runtime, deps subsystemDeps, addr string, noAuth bool, window *desktop.WindowHandle) []serve.Subsystem {
	all := subsystems(rt, deps)
	for i := range all {
		all[i].Auxiliary = true
	}
	return append([]serve.Subsystem{desktopWindowSubsystem(rt, deps, addr, noAuth, window)}, all...)
}

// desktopWindowSubsystem is the loopback surface the window's tab shell talks
// to. It always runs, whatever the configuration enables, because without it
// there is no window; it shares the session manager and the mirroring with
// every other surface, but keeps its own loopback address, its own credential
// policy (none, unless -auth was passed) and the tab shell mount.
func desktopWindowSubsystem(rt *serve.Runtime, deps subsystemDeps, addr string, noAuth bool, window *desktop.WindowHandle) serve.Subsystem {
	at := func(*config.Config) string { return addr }
	return serve.Subsystem{
		Kind:          serve.KindDesktop,
		ConfigKey:     "coddy desktop",
		BuildTag:      "desktop",
		Available:     httpserver.Available,
		NeedsSessions: true,
		// The window is this surface, so the key that decides whether a server
		// runs it does not speak for it: `httpserver.enable: false`, set for a
		// node that only polls a messenger, must not leave a desktop with
		// nothing to open on.
		Enabled: func(*config.Config) bool { return true },
		Address: at,
		// The address is the launcher's and never the file's, so the one
		// change a running server cannot adopt cannot happen here.
		RestartKey: at,
		Run: func(ctx context.Context, cfg *config.Config) error {
			withdrawMirror := func() {}
			withdrawPrompts := func() {}
			return httpserver.Serve(ctx, httpserver.Options{
				Cfg: cfg, Mgr: rt.Mgr, Log: rt.Log,
				DefaultCWD: rt.Paths.CWD, Home: deps.home,
				ListenAddr: addr, Mirror: rt, DetachedPrompts: rt, Wakes: rt,
				NoAuth:          noAuth,
				DesktopWindow:   window,
				UnbuiltSections: unbuiltSections(deps), Status: rt.StatusDocument,
				OnServer: func(s *httpserver.Server) {
					if s == nil {
						withdrawMirror()
						withdrawPrompts()
						return
					}
					// Both HTTP surfaces register as mirrors: a turn started
					// through a gateway, the console or the public surface is
					// live in the window and in a remote browser alike.
					withdrawMirror = rt.AddTurnMirror(s)
					withdrawPrompts = rt.AddDetachedPermissionApprover(s)
				},
			})
		},
	}
}

// desktopConfigAdjust re-applies the launch-time overrides to a configuration
// a reload brings - the desktop's --log-level, and the normalization and
// validation the surfaces it shares with `coddy serve` expect.
func desktopConfigAdjust(logLevel string) func(*config.Config) error {
	return func(c *config.Config) error {
		c.Logger.ApplyOverrides(config.LoggerCLIOverrides{Level: logLevel})
		c.Swarm.Normalize()
		if err := c.Swarm.Validate(); err != nil {
			return err
		}
		return c.Scheduler.Validate(c)
	}
}
