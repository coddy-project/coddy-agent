//go:build desktop

package desktop_test

// Godog harness for features/desktop_window.feature: the launcher's own
// behaviour - the port it remembers, the window it starts, the run it ends and
// the second launch it hands over - driven through desktop.Run with a fake
// browser and a stand-in for the serve pipeline.
//
// The fake browser is this test binary re-executed (see TestHelperBrowser),
// the way internal/hooks/hooktest runs a hook process: no shell script, and it
// behaves the same on every platform. It cannot reproduce the one failure a
// real Chromium has here - a second instance on one profile directory hands
// its window to the first and exits at once - which is exactly why the
// launcher probes the remembered port before it starts anything, and why the
// handover scenario below asserts on the probe rather than on the browser.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/desktop"
	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// helperSentinel is the first argument that turns this test binary into the
// fake browser.
const helperSentinel = "desktop-browser-helper"

// TestHelperBrowser is the fake browser's entry point. The ordinary test run
// passes no positional arguments and skips.
func TestHelperBrowser(t *testing.T) {
	args := make([]string, 0, 4)
	for _, a := range os.Args[1:] {
		// The testing flags are the harness's; "--" is what separates them
		// from the window's own argv.
		if strings.HasPrefix(a, "-test.") || a == "--" {
			continue
		}
		args = append(args, a)
	}
	if len(args) == 0 || args[0] != helperSentinel {
		t.Skip("not the browser helper")
	}
	// Everything after the sentinel is the window's own argv. Record it where
	// the scenario can read it, then stay up until the scenario says the
	// window closed - which is what a browser process does.
	record := os.Getenv("CODDY_TEST_BROWSER_RECORD")
	closeAt := os.Getenv("CODDY_TEST_BROWSER_CLOSE")
	if record != "" {
		_ = os.WriteFile(record, []byte(strings.Join(args[1:], "\n")), 0o600)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if closeAt == "" {
			break
		}
		if _, err := os.Stat(closeAt); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(0)
}

// helperBrowser is the command that re-executes this binary as the window.
func helperBrowser(t *testing.T) desktop.BrowserCommand {
	t.Helper()
	return desktop.BrowserCommand{
		Path: os.Args[0],
		Args: []string{"-test.run=^TestHelperBrowser$", "--", helperSentinel},
		Name: "fake",
	}
}

type windowState struct {
	t *testing.T

	home string
	// recordPath is where the fake browser writes the argv it was started
	// with; closePath is the file whose appearance closes the window.
	recordPath string
	closePath  string

	// servedAddr is the address the stand-in pipeline was asked to serve, and
	// served reports whether it was called at all.
	mu         sync.Mutex
	servedAddr string
	served     bool
	serveEnded bool
	// serveDone is closed when the stand-in pipeline of this scenario has
	// returned. Cancelling its context only asks it to stop, and a scenario
	// that reset the state while the goroutine was still writing it raced
	// under -race -count=3.
	serveDone chan struct{}

	runErr  chan error
	ln      net.Listener
	srv     *http.Server
	cancel  context.CancelFunc
	lastCfg *config.Config
	left    []string
	// before is the configuration as it stood before the decision, so the
	// scenario can see that the decision changed none of it.
	before string

	// secondRun is the launch made while this one's window is open.
	secondRun *windowState

	// The window that takes the chords the browser answers itself, and the
	// shell served over it, which is what the page asks for them through.
	chordWindow *desktop.ChordHandle
	chordServer *httptest.Server

	// The window and the settings the menu of the tray icon acts on.
	menuWindow   *desktop.MenuWindow
	menuSettings *desktop.SettingsStore

	// The window the shell's own routes are driven against, and what the last
	// request to each of them answered.
	shellWindow     *desktop.ShellWindow
	shellServer     *httptest.Server
	lastOpenStatus  int
	lastMoveStatus  int
	lastCloseStatus int
	lastAsked       string
	opensSeen       int
	lastTabStatus   int
	tabsSeen        int
	// menuFolders are the workspaces the launcher is given for the rows of
	// this user's application entry.
	menuFolders []string

	// What a drag of the bar is asked about: where the window stands, what
	// size it has of its own, where the pointer took hold, and the answers.
	dragCur                desktop.WindowRect
	dragRestored           desktop.WindowRect
	dragPointerX           int
	dragPlan               desktop.DragStart
	dragMovedX, dragMovedY int
}

func (s *windowState) reset(t *testing.T) {
	s.stop()
	s.t = t
	s.home = t.TempDir()
	s.recordPath = filepath.Join(t.TempDir(), "argv")
	s.closePath = filepath.Join(t.TempDir(), "closed")
	s.mu.Lock()
	s.servedAddr = ""
	s.served = false
	s.serveEnded = false
	s.serveDone = nil
	s.mu.Unlock()
	s.runErr = nil
	s.lastCfg = nil
	s.left = nil
	s.before = ""
	s.secondRun = nil
	t.Setenv("CODDY_TEST_BROWSER_RECORD", s.recordPath)
	t.Setenv("CODDY_TEST_BROWSER_CLOSE", s.closePath)
}

func (s *windowState) stop() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	// Wait for the stand-in to return before anything is reset: the run it
	// belongs to is over, but its goroutine is not, and the next scenario
	// would write the fields it is still writing.
	s.mu.Lock()
	done := s.serveDone
	s.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}
	if s.srv != nil {
		_ = s.srv.Close()
		s.srv = nil
	}
	if s.ln != nil {
		_ = s.ln.Close()
		s.ln = nil
	}
	if s.chordServer != nil {
		s.chordServer.Close()
		s.chordServer = nil
	}
	if s.shellServer != nil {
		s.shellServer.Close()
		s.shellServer = nil
	}
	s.chordWindow = nil
	s.menuWindow = nil
	s.menuSettings = nil
	s.shellWindow = nil
	s.lastOpenStatus = 0
	s.lastMoveStatus = 0
	s.lastCloseStatus = 0
	s.lastAsked = ""
	s.opensSeen = 0
	s.lastTabStatus = 0
	s.tabsSeen = 0
	s.menuFolders = nil
}

// --- the chords the browser answers itself --------------------------------

func (s *windowState) browserAnswersTheChordsItself() error {
	s.chordWindow = desktop.NewChordHandle()
	s.chordServer = httptest.NewServer(shell.Handler(s.chordWindow))
	return nil
}

// pressesChord is what the passive grab on the window does when the key is
// pressed: the browser never sees it, and the launcher has it instead.
func (s *windowState) pressesChord(keys string) error {
	if s.chordWindow == nil {
		return errors.New("no window to press a key on")
	}
	// "T", or "Shift+Tab": what the scenario names after Ctrl.
	key := keys
	shift := false
	if rest, found := strings.CutPrefix(keys, "Shift+"); found {
		key, shift = rest, true
	}
	chord, ok := desktop.ChordOf(desktop.KeysymOf(key), shift)
	if !ok {
		return fmt.Errorf("Ctrl+%s is not one of the chords the launcher takes", keys)
	}
	s.chordWindow.Press(chord)
	return nil
}

func (s *windowState) stripIsToldTo(what string) error {
	want := map[string]string{
		"open a chat":              "new-tab",
		"close the chat in front":  "close-tab",
		"go to the next chat":      "next-tab",
		"go to the previous chat":  "prev-tab",
		"reload the chat in front": "reload-tab",
	}[what]
	if want == "" {
		return fmt.Errorf("the strip is never told to %q", what)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.chordServer.URL+shell.Prefix+"keys", nil)
	if err != nil {
		return err
	}
	// The page asks from the window's own document, which is what the shell
	// answers chords to.
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("asking for the chord answered %s", res.Status)
	}
	var body struct {
		Chord string `json:"chord"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return err
	}
	if body.Chord != want {
		return fmt.Errorf("the strip was told %q, wanted %q", body.Chord, want)
	}
	return nil
}

// --- the menu, drawn by the bar and by the tray icon ----------------------

// trayCarriesAMenu builds what the tray's menu acts on: a window that can be
// asked, the settings of a home of its own, and the shell over both, which is
// where the bar's own menu comes from.
func (s *windowState) trayCarriesAMenu() error {
	// The autostart entry is a file in the XDG config folder of this session,
	// so the scenario gives it one of its own rather than the operator's.
	s.t.Setenv("XDG_CONFIG_HOME", s.t.TempDir())
	s.menuWindow = desktop.NewMenuWindow()
	handle := desktop.NewWindowHandle()
	handle.Attach(s.menuWindow)
	s.menuSettings = desktop.LoadSettings(s.home, nil)
	s.menuSettings.BindWindow(handle)
	handle.AttachSettings(s.menuSettings)
	// The chord steps read from this one, so a row of the tray's menu that
	// asks the strip for something is checked the way a key is.
	s.chordServer = httptest.NewServer(shell.Handler(handle))
	return nil
}

// barMenuRows is what the page draws, read the way the page reads it.
func (s *windowState) barMenuRows() ([]shell.MenuItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.chordServer.URL+shell.Prefix+"menu", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("asking for the menu answered %s", res.Status)
	}
	var body struct {
		Items []shell.MenuItem `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Items, nil
}

func (s *windowState) trayOffersEveryRowOfTheBar() error {
	bar, err := s.barMenuRows()
	if err != nil {
		return err
	}
	if len(bar) == 0 {
		return errors.New("the bar's menu offers nothing at all")
	}
	tray := s.menuSettings.MenuRows()
	have := make(map[string]string, len(tray))
	for _, row := range tray {
		have[row.ID] = row.Label
	}
	for _, row := range bar {
		label, ok := have[row.ID]
		if !ok {
			return fmt.Errorf("the bar offers %q and the tray does not", row.Label)
		}
		if label != row.Label {
			return fmt.Errorf("the bar calls %q %q and the tray calls it %q", row.ID, row.Label, label)
		}
	}
	return nil
}

func (s *windowState) trayOffersTheWindowAboveThem() error {
	tray := s.menuSettings.MenuRows()
	if len(tray) < 3 {
		return fmt.Errorf("the tray's menu has %d rows, too few to carry the window's own", len(tray))
	}
	if tray[0].ID != shell.MenuShowWindow || tray[1].ID != shell.MenuHideWindow {
		return fmt.Errorf("the tray's menu starts with %q and %q", tray[0].ID, tray[1].ID)
	}
	if tray[2].Kind != shell.MenuSeparator {
		return errors.New("the window's rows are not set apart from the rest")
	}
	return nil
}

// picksFromTheTray is the desktop telling us a row of the icon's menu was
// chosen, by the label the operator read.
func (s *windowState) picksFromTheTray(label string) error {
	for _, row := range s.menuSettings.MenuRows() {
		if row.Label == label {
			return s.menuSettings.PickMenu(row.ID)
		}
	}
	return fmt.Errorf("the tray's menu offers no %q", label)
}

func (s *windowState) windowBroughtBack() error {
	if s.menuWindow.Shown() != 1 {
		return fmt.Errorf("the window was brought back %d times", s.menuWindow.Shown())
	}
	return nil
}

func (s *windowState) windowPutAway() error {
	if s.menuWindow.Hidden() != 1 {
		return fmt.Errorf("the window was put away %d times", s.menuWindow.Hidden())
	}
	return nil
}

func (s *windowState) windowClosed() error {
	if s.menuWindow.Closed() != 1 {
		return fmt.Errorf("the window was closed %d times", s.menuWindow.Closed())
	}
	return nil
}

// --- the shell's own routes, over a window that records what it is asked ---

func (s *windowState) windowOnTheTabShell() error {
	s.shellWindow = desktop.NewShellWindow()
	s.shellServer = httptest.NewServer(shell.Handler(s.shellWindow))
	return nil
}

// shellWithTray binds the real close path to a recording window and a tray
// whose visibility the scenario chooses, without needing a session bus.
func (s *windowState) shellWithTray(visible bool) error {
	s.menuWindow = desktop.NewMenuWindow()
	handle := desktop.NewWindowHandle()
	handle.Attach(s.menuWindow)
	s.menuSettings = desktop.LoadSettings(s.home, nil)
	s.menuSettings.BindWindowAndTray(handle, desktop.NewTrayPresence(visible))
	handle.AttachSettings(s.menuSettings)
	if visible {
		if err := s.menuSettings.SetSettings(desktop.Settings{Tray: true}); err != nil {
			return err
		}
	}
	s.shellServer = httptest.NewServer(shell.Handler(handle))
	return nil
}

func (s *windowState) desktopWindowWithAVisibleTray() error { return s.shellWithTray(true) }
func (s *windowState) desktopWindowWithoutATray() error     { return s.shellWithTray(false) }

func (s *windowState) closeFromTheShell() error {
	code, err := s.askTheWindow("window", map[string]string{"action": "close"})
	if err != nil {
		return err
	}
	s.lastCloseStatus = code
	if code != http.StatusNoContent {
		return fmt.Errorf("closing the window answered %d", code)
	}
	return nil
}

func (s *windowState) windowMinimised() error {
	if s.menuWindow.Hidden() != 1 {
		return fmt.Errorf("the window was minimised %d times", s.menuWindow.Hidden())
	}
	return nil
}

func (s *windowState) windowNotClosed() error {
	if s.menuWindow.Closed() != 0 {
		return fmt.Errorf("the window was closed %d times", s.menuWindow.Closed())
	}
	return nil
}

func (s *windowState) windowNotMinimised() error {
	if s.menuWindow.Hidden() != 0 {
		return fmt.Errorf("the window was minimised %d times", s.menuWindow.Hidden())
	}
	return nil
}

// askTheWindow is one POST the strip makes, answered the way the page's own
// document is answered.
func (s *windowState) askTheWindow(route string, body any) (int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, s.shellServer.URL+shell.Prefix+route, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode, nil
}

func (s *windowState) chatAsksToOpen(url string) error {
	s.opensSeen = len(s.shellWindow.Opened())
	s.lastAsked = url
	code, err := s.askTheWindow("open", map[string]string{"url": url})
	if err != nil {
		return err
	}
	s.lastOpenStatus = code
	return nil
}

func (s *windowState) browserOpenedOnIt() error {
	if s.lastOpenStatus != http.StatusNoContent {
		return fmt.Errorf("the window answered %d", s.lastOpenStatus)
	}
	opened := s.shellWindow.Opened()
	if len(opened) != s.opensSeen+1 || opened[len(opened)-1] != s.lastAsked {
		return fmt.Errorf("this machine's browser was opened on %v", opened)
	}
	return nil
}

func (s *windowState) windowOpensNothing() error {
	if s.lastOpenStatus == http.StatusNoContent {
		return errors.New("the window took the address")
	}
	if opened := s.shellWindow.Opened(); len(opened) != s.opensSeen {
		return fmt.Errorf("this machine's browser was opened on %v", opened)
	}
	return nil
}

// A move that arrives before the window has been told where the drag began is
// refused rather than measured from wherever the last drag left the window:
// the two are separate requests, and the browser is free to deliver them in
// either order.
func (s *windowState) movesBeforeTheDragBegan() error {
	code, err := s.askTheWindow("window", map[string]any{"action": "move-by", "x": 40, "y": 20})
	if err != nil {
		return err
	}
	s.lastMoveStatus = code
	return nil
}

func (s *windowState) windowDoesNotMove() error {
	if s.lastMoveStatus == http.StatusNoContent {
		return errors.New("the window was moved by a drag that had not begun")
	}
	if moves := s.shellWindow.Moves(); len(moves) != 0 {
		return fmt.Errorf("the window was moved %v", moves)
	}
	return nil
}

func (s *windowState) dragBeginsAndThePageAsksAgain() error {
	if code, err := s.askTheWindow("window", map[string]any{"action": "move-begin", "x": 100, "y": 10}); err != nil {
		return err
	} else if code != http.StatusNoContent {
		return fmt.Errorf("the window answered the start of the drag with %d", code)
	}
	code, err := s.askTheWindow("window", map[string]any{"action": "move-by", "x": 40, "y": 20})
	if err != nil {
		return err
	}
	s.lastMoveStatus = code
	return nil
}

func (s *windowState) windowFollowsThePointer() error {
	if s.lastMoveStatus != http.StatusNoContent {
		return fmt.Errorf("the window answered %d", s.lastMoveStatus)
	}
	moves := s.shellWindow.Moves()
	if len(moves) != 1 || moves[0] != [2]int{40, 20} {
		return fmt.Errorf("the window was moved %v", moves)
	}
	return nil
}

// --- the application entry of this user, and the chats its rows open -------

func (s *windowState) asksForAChatInAFolder() error {
	s.tabsSeen = len(s.shellWindow.Tabs())
	s.lastAsked = s.t.TempDir()
	code, err := s.askTheWindow("tab", map[string]string{"cwd": s.lastAsked})
	if err != nil {
		return err
	}
	s.lastTabStatus = code
	return nil
}

func (s *windowState) asksForAChatInAFolderThatIsNotThere() error {
	s.tabsSeen = len(s.shellWindow.Tabs())
	code, err := s.askTheWindow("tab", map[string]string{"cwd": filepath.Join(s.t.TempDir(), "gone")})
	if err != nil {
		return err
	}
	s.lastTabStatus = code
	return nil
}

func (s *windowState) stripOpensAChatThere() error {
	if s.lastTabStatus != http.StatusNoContent {
		return fmt.Errorf("the window answered %d", s.lastTabStatus)
	}
	tabs := s.shellWindow.Tabs()
	if len(tabs) != s.tabsSeen+1 || tabs[len(tabs)-1] != s.lastAsked {
		return fmt.Errorf("the strip was told to open %v", tabs)
	}
	return nil
}

func (s *windowState) windowRefusesAndOpensNothing() error {
	if s.lastTabStatus == http.StatusNoContent {
		return errors.New("the window took a folder that is not there")
	}
	if tabs := s.shellWindow.Tabs(); len(tabs) != s.tabsSeen {
		return fmt.Errorf("the strip was told to open %v", tabs)
	}
	return nil
}

func (s *windowState) twoChatsInFoldersOfTheirOwn() error {
	first, second := s.t.TempDir(), s.t.TempDir()
	s.menuFolders = []string{first, second}
	// The entry is written into this test's own data directory, not the
	// operator's application menu.
	s.t.Setenv("XDG_DATA_HOME", s.t.TempDir())
	return nil
}

func (s *windowState) entryOpensCoddy() error {
	text, err := s.menuEntryText()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	for _, want := range []string{"[Desktop Entry]", "Name=Coddy", exe + " desktop", "StartupWMClass=" + desktop.WindowClass} {
		if !strings.Contains(text, want) {
			return fmt.Errorf("the entry has no %q in it:\n%s", want, text)
		}
	}
	return nil
}

func (s *windowState) entryOffersBothFolders() error {
	text, err := s.menuEntryText()
	if err != nil {
		return err
	}
	for i, folder := range s.menuFolders {
		row := fmt.Sprintf("[Desktop Action folder%d]", i)
		if !strings.Contains(text, row) {
			return fmt.Errorf("the entry has no %s:\n%s", row, text)
		}
		if !strings.Contains(text, "--new-chat --cwd "+folder) {
			return fmt.Errorf("the entry opens no chat in %q:\n%s", folder, text)
		}
	}
	return nil
}

func (s *windowState) menuEntryText() (string, error) {
	path := filepath.Join(os.Getenv("XDG_DATA_HOME"), "applications", "coddy-desktop.desktop")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("this user has no application entry: %w", err)
	}
	return string(b), nil
}

func (s *windowState) switchWrittenDown() error {
	b, err := os.ReadFile(filepath.Join(s.home, "desktop", "settings.json"))
	if err != nil {
		return fmt.Errorf("the switch was not written down: %w", err)
	}
	var saved struct {
		Autostart bool `json:"autostart"`
	}
	if err := json.Unmarshal(b, &saved); err != nil {
		return err
	}
	if !saved.Autostart {
		return errors.New("the settings of this home do not have it on")
	}
	return nil
}

func (s *windowState) barDrawsItTicked() error {
	bar, err := s.barMenuRows()
	if err != nil {
		return err
	}
	for _, row := range bar {
		if row.ID == shell.MenuAutostart {
			if !row.Checked {
				return errors.New("the bar's menu draws it unticked")
			}
			return nil
		}
	}
	return errors.New("the bar's menu no longer offers it")
}

// serveStandIn is what the launcher runs behind the window: a real listener on
// the address it was given, answering the shell page, so the probe a second
// launch makes finds a Coddy desktop here and not merely an open port.
func (s *windowState) serveStandIn(ctx context.Context, addr string) error {
	s.mu.Lock()
	s.servedAddr = addr
	s.served = true
	s.mu.Unlock()

	done := make(chan struct{})
	s.mu.Lock()
	s.serveDone = done
	s.mu.Unlock()
	defer close(done)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle(shell.Prefix, shell.Handler(nil))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	s.ln, s.srv = ln, srv
	go func() { _ = srv.Serve(ln) }()
	<-ctx.Done()
	_ = srv.Close()
	s.mu.Lock()
	s.serveEnded = true
	s.mu.Unlock()
	return nil
}

func (s *windowState) options() desktop.Options {
	return desktop.Options{
		Home:          s.home,
		Window:        desktop.WindowBrowser,
		Browser:       helperBrowser(s.t),
		Serve:         s.serveStandIn,
		RecentFolders: s.menuFolders,
	}
}

// launch starts a run in the background and returns once the window is up, so
// the scenario can act on a desktop that is actually open.
func (s *windowState) launch() error {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	errs := make(chan error, 1)
	go func() { errs <- desktop.Run(ctx, s.options()) }()
	s.runErr = errs
	return s.waitForWindow()
}

func (s *windowState) waitForWindow() error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(s.recordPath); err == nil && len(b) > 0 {
			return nil
		}
		select {
		case err := <-s.runErr:
			s.runErr = nil
			return fmt.Errorf("the run ended before a window opened: %v", err)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("no window opened within the bound")
}

func (s *windowState) windowArgv() ([]string, error) {
	b, err := os.ReadFile(s.recordPath)
	if err != nil {
		return nil, fmt.Errorf("the window recorded no arguments: %w", err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n"), nil
}

// --- Given ---

func (s *windowState) neverOpenedBefore() error {
	if _, err := os.Stat(desktop.PortFile(s.home)); !os.IsNotExist(err) {
		return errors.New("this home already remembers a port")
	}
	return nil
}

func (s *windowState) openedBefore() error {
	// A previous launch is exactly the file one leaves behind.
	port, err := freePort()
	if err != nil {
		return err
	}
	return desktop.WritePort(s.home, port)
}

func (s *windowState) windowOpen() error {
	return s.launch()
}

func (s *windowState) configWithRelayAndScheduler() error {
	cfg := &config.Config{}
	cfg.Swarm.Enabled = true
	cfg.Scheduler.Enabled = true
	s.lastCfg = cfg
	return nil
}

// --- When ---

func (s *windowState) opensTheDesktop() error {
	return s.launch()
}

func (s *windowState) windowCloses() error {
	return os.WriteFile(s.closePath, []byte("x"), 0o600)
}

func (s *windowState) opensTheDesktopAgain() error {
	// The first run keeps its window; this one gets a home that already has a
	// desktop serving on the remembered port.
	//
	// Its fake browser is told to exit as soon as it has recorded the window
	// it was asked for, which is what a real Chromium does here: a second
	// --app against a profile directory the first window holds hands the URL
	// to that window and ends, and this launch is finished the moment it has
	// asked for one.
	handed := filepath.Join(s.t.TempDir(), "handed-over")
	if err := os.WriteFile(handed, []byte("x"), 0o600); err != nil {
		return err
	}
	second := &windowState{
		t:          s.t,
		home:       s.home,
		recordPath: filepath.Join(s.t.TempDir(), "argv2"),
		closePath:  handed,
	}
	s.t.Setenv("CODDY_TEST_BROWSER_RECORD", second.recordPath)
	s.t.Setenv("CODDY_TEST_BROWSER_CLOSE", handed)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := desktop.Run(ctx, second.options()); err != nil {
		return err
	}
	s.secondRun = second
	return nil
}

func (s *windowState) decidesWhatToRun() error {
	if s.lastCfg == nil {
		return errors.New("no configuration was given")
	}
	s.before = fmt.Sprintf("%+v", *s.lastCfg)
	s.left = desktop.SurfacesLeftToServe(s.lastCfg)
	return nil
}

// --- Then ---

func (s *windowState) runsOnALoopbackPortOfItsOwn() error {
	s.mu.Lock()
	addr, served := s.servedAddr, s.served
	s.mu.Unlock()
	if !served {
		return errors.New("nothing was served")
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("the served address %q is not host:port: %w", addr, err)
	}
	if host != "127.0.0.1" {
		return fmt.Errorf("the desktop served %q, which is not loopback", host)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return fmt.Errorf("the desktop served port %q", portStr)
	}
	return nil
}

func (s *windowState) portWrittenDown() error {
	port, err := desktop.ReadPort(s.home)
	if err != nil {
		return fmt.Errorf("no port was written down: %w", err)
	}
	s.mu.Lock()
	addr := s.servedAddr
	s.mu.Unlock()
	if _, served, _ := net.SplitHostPort(addr); served != strconv.Itoa(port) {
		return fmt.Errorf("the desktop served %q but wrote down %d", addr, port)
	}
	return nil
}

func (s *windowState) browserOpenedOnTheShell() error {
	argv, err := s.windowArgv()
	if err != nil {
		return err
	}
	s.mu.Lock()
	addr := s.servedAddr
	s.mu.Unlock()
	want := "--app=http://" + addr + shell.Prefix
	profile := filepath.Join(s.home, "desktop", "profiles", "fake")
	var sawApp, sawProfile bool
	for _, a := range argv {
		if a == want {
			sawApp = true
		}
		if a == "--user-data-dir="+profile {
			sawProfile = true
		}
	}
	if !sawApp {
		return fmt.Errorf("the window was not opened on %q, only %v", want, argv)
	}
	if !sawProfile {
		return fmt.Errorf("the window got no profile of its own, only %v", argv)
	}
	return nil
}

func (s *windowState) runsOnThePortOfTheLastLaunch() error {
	port, err := desktop.ReadPort(s.home)
	if err != nil {
		return err
	}
	s.mu.Lock()
	addr := s.servedAddr
	s.mu.Unlock()
	if _, got, _ := net.SplitHostPort(addr); got != strconv.Itoa(port) {
		return fmt.Errorf("the desktop served %q, not the remembered port %d", addr, port)
	}
	return nil
}

func (s *windowState) runEndsAndServeStops() error {
	if s.runErr == nil {
		return errors.New("no run is in flight")
	}
	select {
	case err := <-s.runErr:
		s.runErr = nil
		if err != nil {
			return fmt.Errorf("the run ended with %v", err)
		}
	case <-time.After(20 * time.Second):
		return errors.New("the run did not end when the window closed")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		ended := s.serveEnded
		s.mu.Unlock()
		if ended {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("the HTTP surface was still running after the run ended")
}

func (s *windowState) secondRunServedNothing() error {
	if s.secondRun == nil {
		return errors.New("there was no second run")
	}
	s.secondRun.mu.Lock()
	served := s.secondRun.served
	s.secondRun.mu.Unlock()
	if served {
		return errors.New("the second run served a surface of its own")
	}
	return nil
}

func (s *windowState) secondRunOpenedTheRunningDesktop() error {
	argv, err := s.secondRun.windowArgv()
	if err != nil {
		return err
	}
	s.mu.Lock()
	addr := s.servedAddr
	s.mu.Unlock()
	want := "--app=http://" + addr + shell.Prefix
	for _, a := range argv {
		if a == want {
			return nil
		}
	}
	return fmt.Errorf("the second window opened on %v, not on the running desktop at %q", argv, want)
}

// The window runs one surface because it is handed one subsystem descriptor,
// never because the configuration was edited underneath the operator: the
// settings screen inside the window draws the live document, so a value this
// process cleared is the value a save from that screen would write to disk.
func (s *windowState) changesNothingInTheConfig() error {
	if s.before == "" {
		return errors.New("nothing was read before the decision")
	}
	if after := fmt.Sprintf("%+v", *s.lastCfg); after != s.before {
		return fmt.Errorf("the desktop edited the operator's configuration:\n before %s\n after  %s", s.before, after)
	}
	return nil
}

func (s *windowState) namesWhatItLeft() error {
	if len(s.left) == 0 {
		return errors.New("the desktop named none of the surfaces it turned off")
	}
	joined := strings.Join(s.left, " ")
	for _, want := range []string{"swarm", "scheduler"} {
		if !strings.Contains(joined, want) {
			return fmt.Errorf("the desktop did not name %q among %v", want, s.left)
		}
	}
	return nil
}

// --- dragging the bar -------------------------------------------------------

// The window manager's side of a drag needs a display and a running manager,
// so what the scenarios hold is the arithmetic: whether a pinned window is let
// loose, where it is put back, and where a move may leave it.

func (s *windowState) pinnedToTheLeftHalf() error {
	// A quick-tiled window as KWin leaves it on this desktop: half the width
	// of the screen, the height of the working area. Its own size is narrower,
	// which is what makes the grab point move when it is let loose.
	s.dragCur = desktop.WindowRect{X: 0, Y: 74, W: 1280, H: 1316}
	s.dragRestored = desktop.WindowRect{X: 360, Y: 137, W: 800, H: 900}
	return nil
}

func (s *windowState) grabbedThreeQuartersAlong() error {
	s.dragPointerX = s.dragCur.X + s.dragCur.W*3/4
	s.dragPlan = desktop.PlanDragStart(true, s.dragCur, s.dragRestored, s.dragPointerX)
	return nil
}

func (s *windowState) letLooseFirst() error {
	if !s.dragPlan.Restore {
		return errors.New("the drag left the window pinned, so the window manager would refuse every move of it")
	}
	return nil
}

func (s *windowState) putBackUnderThePointer() error {
	grab := s.dragPointerX - s.dragPlan.X
	want := s.dragRestored.W * 3 / 4
	if grab != want {
		return fmt.Errorf("the pointer ended up %d into a window %d wide, not %d: the window jumped out from under it",
			grab, s.dragRestored.W, want)
	}
	return nil
}

func (s *windowState) beingDraggedByItsBar() error {
	s.dragCur = desktop.WindowRect{X: 40, Y: 120, W: 900, H: 700}
	s.dragPlan = desktop.PlanDragStart(false, s.dragCur, s.dragCur, s.dragCur.X+100)
	return nil
}

func (s *windowState) pointerPastTheLeftEdge() error {
	// The pointer has carried the window 300 pixels further left than the
	// screen begins.
	s.dragMovedX, s.dragMovedY = desktop.ClampMove(s.dragPlan.X-340, s.dragPlan.Y+20)
	return nil
}

func (s *windowState) followsItOffTheEdge() error {
	if want := s.dragCur.X - 340; s.dragMovedX != want {
		return fmt.Errorf("the window stopped at x=%d instead of following the pointer to %d", s.dragMovedX, want)
	}
	return nil
}

func (s *windowState) neverAboveTheTop() error {
	if x, y := desktop.ClampMove(s.dragCur.X, -200); y != 0 || x != s.dragCur.X {
		return fmt.Errorf("a drag put the window at (%d, %d), with its bar off the top of the screen", x, y)
	}
	return nil
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func TestDesktopWindowFeature(t *testing.T) {
	st := &windowState{}
	suite := godog.TestSuite{
		Name: "desktop-window",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				st.reset(t)
				return ctx, nil
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				st.stop()
				return ctx, nil
			})

			sc.Step(`^a machine where Coddy has never opened a desktop window$`, st.neverOpenedBefore)
			sc.Step(`^a machine where Coddy has opened a desktop window before$`, st.openedBefore)
			sc.Step(`^a desktop window open on its own Coddy$`, st.windowOpen)
			sc.Step(`^a configuration that also enables the swarm relay and the scheduler$`, st.configWithRelayAndScheduler)

			sc.Step(`^the operator opens the desktop$`, st.opensTheDesktop)
			sc.Step(`^the operator opens the desktop again$`, st.opensTheDesktopAgain)
			sc.Step(`^the window closes$`, st.windowCloses)
			sc.Step(`^the desktop decides what to run$`, st.decidesWhatToRun)

			sc.Step(`^the HTTP surface runs on a loopback port of its own$`, st.runsOnALoopbackPortOfItsOwn)
			sc.Step(`^that port is written down for the next launch$`, st.portWrittenDown)
			sc.Step(`^the browser is opened on the tab shell of that port, in a profile of its own$`, st.browserOpenedOnTheShell)
			sc.Step(`^the HTTP surface runs on the port of the last launch$`, st.runsOnThePortOfTheLastLaunch)
			sc.Step(`^the run ends and the HTTP surface stops with it$`, st.runEndsAndServeStops)
			sc.Step(`^the second run serves nothing of its own$`, st.secondRunServedNothing)
			sc.Step(`^it opens the tab shell of the desktop already running$`, st.secondRunOpenedTheRunningDesktop)
			sc.Step("^it names the surfaces it left to `coddy serve`$", st.namesWhatItLeft)
			sc.Step(`^it changes nothing in the configuration itself$`, st.changesNothingInTheConfig)

			sc.Step(`^a window whose browser answers Ctrl\+T and Ctrl\+W itself$`, st.browserAnswersTheChordsItself)
			sc.Step(`^the operator presses Ctrl\+((?:Shift\+)?(?:[A-Z]|Tab))$`, st.pressesChord)
			sc.Step(`^the strip is told to (.+)$`, st.stripIsToldTo)

			sc.Step(`^a desktop window whose tray icon carries a menu$`, st.trayCarriesAMenu)
			sc.Step(`^the tray's menu offers every row the bar's menu offers$`, st.trayOffersEveryRowOfTheBar)
			sc.Step(`^above them it offers showing the window and hiding it$`, st.trayOffersTheWindowAboveThem)
			sc.Step(`^the operator picks "([^"]+)" from the tray$`, st.picksFromTheTray)
			sc.Step(`^the window is brought back$`, st.windowBroughtBack)
			sc.Step(`^the window is put away$`, st.windowPutAway)
			sc.Step(`^the window is closed$`, st.windowClosed)
			sc.Step(`^a desktop window with a visible tray icon$`, st.desktopWindowWithAVisibleTray)
			sc.Step(`^a desktop window without a tray icon$`, st.desktopWindowWithoutATray)
			sc.Step(`^the operator closes the window from the shell$`, st.closeFromTheShell)
			sc.Step(`^the window is minimised$`, st.windowMinimised)
			sc.Step(`^the window is not closed$`, st.windowNotClosed)
			sc.Step(`^the window is closed once$`, st.windowClosed)
			sc.Step(`^the window is not minimised$`, st.windowNotMinimised)

			sc.Step(`^a window on the tab shell$`, st.windowOnTheTabShell)
			sc.Step(`^a chat asks the window to open "([^"]*)"$`, st.chatAsksToOpen)
			sc.Step(`^this machine's own browser is opened on it$`, st.browserOpenedOnIt)
			sc.Step(`^the window opens nothing$`, st.windowOpensNothing)
			sc.Step(`^the application menu asks for a chat in a folder of this machine$`, st.asksForAChatInAFolder)
			sc.Step(`^the application menu asks for a chat in a folder that is not there$`, st.asksForAChatInAFolderThatIsNotThere)
			sc.Step(`^a chat is opened in that folder$`, st.stripOpensAChatThere)
			sc.Step(`^the window refuses, and the strip is told nothing$`, st.windowRefusesAndOpensNothing)
			sc.Step(`^two chats of this home were had in folders of their own$`, st.twoChatsInFoldersOfTheirOwn)
			sc.Step(`^this user's application entry opens Coddy and names the window's class$`, st.entryOpensCoddy)
			sc.Step(`^it offers both folders, each opening a chat of its own$`, st.entryOffersBothFolders)
			sc.Step(`^the page asks to move the window before saying where the drag began$`, st.movesBeforeTheDragBegan)
			sc.Step(`^the window does not move$`, st.windowDoesNotMove)
			sc.Step(`^the drag begins and the page asks again$`, st.dragBeginsAndThePageAsksAgain)
			sc.Step(`^the window follows the pointer$`, st.windowFollowsThePointer)
			sc.Step(`^the switch is written down for the next launch$`, st.switchWrittenDown)
			sc.Step(`^the bar's menu draws it ticked$`, st.barDrawsItTicked)

			sc.Step(`^a window the window manager has pinned to the left half of the screen$`, st.pinnedToTheLeftHalf)
			sc.Step(`^the bar is grabbed three quarters along it and dragged$`, st.grabbedThreeQuartersAlong)
			sc.Step(`^the window is let loose before it follows the pointer$`, st.letLooseFirst)
			sc.Step(`^it is put back under the pointer, three quarters along its own width$`, st.putBackUnderThePointer)
			sc.Step(`^a window being dragged by its bar$`, st.beingDraggedByItsBar)
			sc.Step(`^the pointer carries it past the left edge of the screen$`, st.pointerPastTheLeftEdge)
			sc.Step(`^the window follows it off the edge$`, st.followsItOffTheEdge)
			sc.Step(`^a drag never puts the bar above the top of the screen$`, st.neverAboveTheTop)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/desktop_window.feature"},
			TestingT: t,
			Output:   os.Stdout,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("desktop window feature failed")
	}
}
