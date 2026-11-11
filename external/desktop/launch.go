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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
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
	// NewChat asks for a chat in CWD rather than for whatever the window was
	// last left on. A launch that finds a desktop already serving hands it
	// over instead of opening a second window, which is what a row of the
	// task bar's menu naming a recent workspace does.
	NewChat bool
	// CWD is the folder that chat opens in, and the one the entry's rows are
	// written from. Empty leaves the window on its own default.
	CWD string
	// RecentFolders are the workspaces of this home's recent chats, most
	// recent first: the rows of the application entry's menu. The caller
	// reads them off the session store, which this package does not know.
	RecentFolders []string
}

// Run serves Coddy on this machine's desktop port and opens a window on it,
// returning when the desktop run ends.
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
	// A real window close ends the run, and Quit in the tray's menu is the
	// other way to end it: a window minimised into the tray still has to let
	// go. The shell's close button may instead minimise when the tray is shown.
	runCtx, endRun := context.WithCancel(ctx)
	defer endRun()
	settings.bindQuit(endRun)
	saved := settings.Settings()
	opts.Framed = opts.Framed || saved.Framed
	opts.StartMinimised = opts.StartMinimised || saved.StartMinimised

	// The entry of this user names the running binary and carries the folders
	// of its recent chats, so the task bar draws Coddy's own icon and menu
	// even where no package is installed (menu_entry_linux.go).
	writeEntry(opts, log)

	if addr, ok := runningDesktop(runCtx, opts.Home); ok {
		if opts.NewChat {
			if err := askForTab(runCtx, addr, opts.CWD); err != nil {
				log.Warn("the desktop already running would not open a chat", "addr", addr, "error", err)
			}
		}
		log.Info("a Coddy desktop is already serving here, opening a window on it", "addr", addr)
		return finishWindow(opts, openWindow(runCtx, opts, shellURL(addr)), shellURL(addr), nil, runCtx)
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
	// A switch is drawn in two menus, and only one of them was thrown: the
	// tray redraws from the store, and the page is told to read it again
	// through the queue it is already holding a poll on.
	settings.watch(func() {
		tray.menuChanged()
		if opts.Handle != nil {
			opts.Handle.RequestChord(chordSettingsMoved)
		}
	})
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

	serveCtx, stopServe := context.WithCancel(runCtx)
	defer stopServe()
	serveErr := make(chan error, 1)
	go func() { serveErr <- opts.Serve(serveCtx, addr) }()

	bound := opts.ReadyTimeout
	if bound <= 0 {
		bound = DefaultReadyTimeout
	}
	if err := waitForListener(runCtx, addr, serveErr, bound); err != nil {
		stopServe()
		return err
	}
	log.Info("the desktop is serving", "addr", addr)

	url := shellURL(addr)
	windowErr := openWindow(runCtx, opts, url)
	err = finishWindow(opts, windowErr, url, serveErr, runCtx)
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

// writeEntry writes this user's application entry. A menu that could not be
// written is a menu that looks as it did before, which is not worth a word
// above debug and never worth refusing to open a window over.
func writeEntry(opts Options, log *slog.Logger) {
	exe, err := os.Executable()
	if err != nil {
		log.Debug("the application entry needs this program's own path", "error", err)
		return
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := writeMenuEntry(exe, opts.RecentFolders); err != nil {
		log.Debug("the application entry could not be written", "error", err)
	}
}

// askForTab tells the desktop already running to open a chat in a folder.
func askForTab(ctx context.Context, addr, cwd string) error {
	body, err := json.Marshal(map[string]string{"cwd": cwd})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+shell.Prefix+"tab", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// The window's own page is what drives it; this is the same machine and
	// the same person, asking for the thing a row of their menu names.
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("the window answered %s", res.Status)
	}
	return nil
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
