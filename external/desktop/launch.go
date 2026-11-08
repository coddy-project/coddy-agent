//go:build desktop

// Package desktop opens Coddy as a window on this machine: the HTTP surface on
// a loopback port the home remembers, and a window on the tab shell it serves.
//
// The package is the launcher only. What runs behind the window is handed in
// as Options.Serve, so nothing here imports the HTTP server and the whole
// lifetime - the port, the readiness, the hand-over to a desktop already
// running, the end of the run - is testable with a stand-in.
package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// DefaultReadyTimeout bounds the wait for the surface behind the window to
// start listening. It is generous because the first start of a session store
// on a cold disk is not instant, and because the alternative - opening the
// window on a connection refused - is worse than waiting.
const DefaultReadyTimeout = 30 * time.Second

// Options are what a run needs. Home and Serve are required.
type Options struct {
	// Home is the agent home. The remembered port and the window's browser
	// profile live under it.
	Home string
	// Window names the backend: WindowAuto (the default), WindowBrowser or
	// WindowNative.
	Window string
	// Browser names the browser to open the window with, overriding both the
	// environment and the search. A test puts its own fake here.
	Browser BrowserCommand
	// Serve runs the surface on addr and returns when ctx ends. The launcher
	// cancels that context when the window closes.
	Serve func(ctx context.Context, addr string) error
	// Log is the process logger; nil discards.
	Log *slog.Logger
	// Out is where the launcher says what it did; nil is os.Stderr.
	Out io.Writer
	// ReadyTimeout overrides DefaultReadyTimeout.
	ReadyTimeout time.Duration
	// Handle is where the launcher puts the window once the browser has one,
	// so the tab strip can drive it. The same handle is given to the server,
	// which serves the strip before any window exists.
	Handle *WindowHandle
	// Framed keeps the system title bar and borders. Without it the window
	// opens as a bare rectangle and the tab strip is its title bar: the drag,
	// the buttons and the menu are all in the strip. The switch in the menu
	// sets the same thing for the launches that follow.
	Framed bool
	// StartMinimised opens the window minimised, which is what a window opened
	// by the session's autostart usually wants. It is set from the menu, not
	// from the command line.
	StartMinimised bool
}

// Run serves Coddy on this machine's desktop port and opens a window on it,
// returning when that window closes.
//
// A desktop already serving on the remembered port is handed the window
// instead: the second launch starts nothing of its own, which is what the
// operator means by starting the application twice.
func Run(ctx context.Context, opts Options) error {
	if strings.TrimSpace(opts.Home) == "" {
		return errors.New("desktop: the agent home is required")
	}
	log := logger(opts.Log)

	// The switches of the menu are the window's own, kept under the home: the
	// frame, the tray icon, the autostart entry and whether this launch opens
	// minimised. The command line still wins for the frame, so a window opened
	// with --frame is framed whatever the file says.
	settings := LoadSettings(opts.Home, log)
	if opts.Handle != nil {
		opts.Handle.AttachSettings(settings)
	}
	saved := settings.Settings()
	opts.Framed = opts.Framed || saved.Framed
	opts.StartMinimised = opts.StartMinimised || saved.StartMinimised

	if addr, ok := runningDesktop(ctx, opts.Home); ok {
		log.Info("a Coddy desktop is already serving here, opening a window on it", "addr", addr)
		return finishWindow(opts, openWindow(ctx, opts, shellURL(addr)), shellURL(addr), nil, ctx)
	}

	if opts.Serve == nil {
		return errors.New("desktop: nothing was given to serve behind the window")
	}

	// The tray icon belongs to the run that serves, so a second launch - which
	// only hands its window over and ends - never puts a second icon in the
	// tray.
	tray := newTrayIcon(log, func() {
		if opts.Handle == nil {
			return
		}
		if err := opts.Handle.Activate(); err != nil {
			log.Debug("the window did not come back from the tray", "error", err)
		}
	}, newTrayMenu(settings.MenuRows, settings.PickMenu))
	settings.bind(opts.Handle, tray)
	defer tray.Hide()
	if saved.Tray {
		if err := tray.Show(); err != nil {
			log.Warn("the tray icon could not be shown", "error", err)
		}
	}
	port, err := choosePort(opts.Home, log)
	if err != nil {
		return err
	}
	addr := loopbackAddr(port)

	serveCtx, stopServe := context.WithCancel(ctx)
	defer stopServe()
	serveErr := make(chan error, 1)
	go func() { serveErr <- opts.Serve(serveCtx, addr) }()

	bound := opts.ReadyTimeout
	if bound <= 0 {
		bound = DefaultReadyTimeout
	}
	if err := waitForListener(ctx, addr, serveErr, bound); err != nil {
		stopServe()
		return err
	}
	log.Info("the desktop is serving", "addr", addr)

	url := shellURL(addr)
	windowErr := openWindow(ctx, opts, url)
	err = finishWindow(opts, windowErr, url, serveErr, ctx)
	// The surface exists for the window: when the window is gone, so is the
	// reason to keep serving, and the process ends with both.
	stopServe()
	select {
	case stopped := <-serveErr:
		if stopped != nil {
			log.Debug("the surface behind the window stopped with an error", "error", stopped)
		}
	case <-time.After(5 * time.Second):
		log.Warn("the surface behind the window did not stop within five seconds")
	}
	return err
}

// finishWindow decides what a closed - or never opened - window means.
//
// A machine with no window this run can wait on is not a failure: Coddy is
// serving, the address is printed, and the run lasts until a signal, the way
// `coddy serve` does. serveErr may be nil, which is the hand-over case: there
// is no surface of our own to outlive.
func finishWindow(opts Options, windowErr error, url string, serveErr <-chan error, ctx context.Context) error {
	if !errors.Is(windowErr, ErrNoWaitableWindow) {
		return windowErr
	}
	out := opts.Out
	if out == nil {
		out = os.Stderr
	}
	_, _ = fmt.Fprintf(out, "No window could be opened on this machine, so Coddy is only serving.\n  open %s in a browser\n  Ctrl+C stops it\n", url)
	if serveErr == nil {
		return nil
	}
	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		return nil
	}
}

// runningDesktop answers the address of the desktop already serving for this
// home, if there is one.
func runningDesktop(ctx context.Context, home string) (string, bool) {
	port, err := ReadPort(home)
	if err != nil || port <= 0 {
		return "", false
	}
	addr := loopbackAddr(port)
	if portFree(port) {
		return "", false
	}
	return addr, desktopServingAt(ctx, addr)
}

// choosePort answers the port to serve on: the remembered one while this
// machine will still give it to us, else a fresh one, which is then what the
// home remembers.
//
// Losing the remembered port costs one launch its tab list, because the
// browser keeps that list per origin and the origin is the port. That is worth
// one line in the log and nothing more - refusing to start because some other
// program took the port would be worse.
func choosePort(home string, log *slog.Logger) (int, error) {
	if port, err := ReadPort(home); err == nil && port > 0 {
		// The desktop that had this port may still be stopping - the window is
		// closed, its surface is not quite down - and that launch is usually
		// the operator starting the application again. Waiting a moment for it
		// is what keeps their tabs.
		if portFreeWithin(port, portHandover) {
			return port, nil
		}
		log.Warn("the desktop port of this home is taken by something that is not a Coddy desktop; taking another one, and this launch starts with no tabs", "port", port)
	}
	port, err := freeLoopbackPort()
	if err != nil {
		return 0, fmt.Errorf("desktop: no loopback port is free: %w", err)
	}
	if err := WritePort(home, port); err != nil {
		return 0, fmt.Errorf("desktop: remembering the port: %w", err)
	}
	return port, nil
}

// shellURL is where a window opens: the tab shell, never a chat directly. The
// shell is what restores the tabs of the last session.
func shellURL(addr string) string { return "http://" + addr + shell.Prefix }

// SurfacesLeftToServe names the subsystems this configuration enables that the
// window does not run, by their configuration key.
//
// It reads and changes nothing. That is the whole point: the settings screen
// inside the window draws the live configuration (`configapi.Backend.Live`),
// so a value this process had quietly turned off would be the value a save
// from that screen wrote to disk - the operator's own swarm relay, their bot,
// their sign-in account, gone because they changed a theme in a desktop
// window. The window runs one surface because it is handed one subsystem
// descriptor and `serve.Resolve` consults no other, not because the document
// was edited underneath it.
//
// They are left to `coddy serve` for a reason beyond redundancy: the
// supervisor cancels the whole run on one subsystem's error, so a second swarm
// relay that cannot bind its port would take the window down with it; two
// Telegram pollers on one token make the Bot API answer 409 and one bot goes
// deaf; two schedulers fire every cron job twice.
func SurfacesLeftToServe(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	var left []string
	for _, s := range []struct {
		key string
		on  bool
	}{
		{"gateways.telegram.enable", cfg.Gateways.Telegram.Enabled},
		{"gateways.pachca.enable", cfg.Gateways.Pachca.Enabled},
		{"swarm.enable", cfg.Swarm.Enabled},
		{"scheduler.enable", cfg.Scheduler.Enabled},
		{"metrics.enable", cfg.Metrics.Enable},
		{"browser.enable", cfg.Browser.Enable},
	} {
		if s.on {
			left = append(left, s.key)
		}
	}
	return left
}
