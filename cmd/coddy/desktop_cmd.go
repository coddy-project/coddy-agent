//go:build desktop && http && ui

// `coddy desktop` opens Coddy as a window on this machine.
//
// The window is the operator's Chromium-family browser in app mode, in a
// profile of its own, over an HTTP surface started on a loopback port the
// agent home remembers; the page it opens on is a tab strip with one chat per
// tab (external/desktop/shell). The whole of Coddy is behind it - the same
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
// The native window around the system WebView arrives with the desktop.native
// tag, which needs cgo.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
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

// desktopHiddenSections are the settings sections the window does not offer.
//
// They are the surfaces of `coddy serve` - a bot polling a messenger, a relay,
// a cron daemon, an exporter - and the window runs none of them: it is handed
// one subsystem descriptor and serve.Resolve consults no other. Offering a
// switch the window ignores would be worse than not offering it, so its
// settings form does not draw them whatever tags the binary was built with.
var desktopHiddenSections = []string{"gateways", "swarm", "scheduler", "metrics", "browser"}

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
	framed := fs.Bool("frame", false, "keep the system title bar and borders. Without it the window is a bare rectangle and the tab strip is its title bar: it carries the window's own buttons and is what you drag it by (X11 sessions; elsewhere the frame stays)")
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

	// The window runs the web interface and nothing else, and it says so once.
	// Nothing in the configuration is changed to achieve that: the surface it
	// runs is the one subsystem descriptor serveHTTP hands the supervisor, and
	// the document stays exactly as the operator wrote it - which is what the
	// settings screen inside the window reads, and therefore what a save from
	// that screen writes back.
	if left := desktop.SurfacesLeftToServe(cfg); len(left) > 0 {
		fmt.Fprintf(os.Stderr, "coddy desktop runs the web interface only; left to `coddy serve`: %s\n", strings.Join(left, ", "))
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
	log.Info("starting the desktop window", "version", version.Get(), "config", paths.ConfigPath, "workspace", paths.CWD)
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
		Home:          paths.Home,
		Window:        strings.TrimSpace(*window),
		Browser:       desktop.BrowserCommand{Path: strings.TrimSpace(*browser)},
		Log:           log,
		Handle:        windowHandle,
		Framed:        *framed,
		NewChat:       *newChat,
		CWD:           paths.CWD,
		RecentFolders: recentWorkspaces(rt, log),
		Serve:         serveHTTP(rt, paths.Home, !*keepAuth, windowHandle),
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
					UnbuiltSections: desktopHiddenSections, Status: rt.StatusDocument,
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
