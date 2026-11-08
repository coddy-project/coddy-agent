//go:build desktop

package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
)

// Settings are the switches of the menu in the bar.
type Settings = shell.Settings

// settingsFile is where they are kept, under the agent home: these outlive a
// document, and the browser scopes its own storage to the origin, which
// carries the port and therefore moves.
const settingsFile = "settings.json"

// SettingsStore keeps the window's switches and applies a change to this
// machine: the frame comes off or goes back on, the tray icon appears, the
// autostart entry of this session is written or removed.
type SettingsStore struct {
	path string
	log  *slog.Logger

	mu  sync.Mutex
	cur Settings

	// win is what a frame change is asked of, and tray what a tray change is.
	// Both are attached by the launcher once the window exists, so a switch
	// thrown before then is still remembered and applied when it does.
	win  *WindowHandle
	tray *trayIcon
	// quit ends the run, for the row of the tray's menu that says so.
	quit func()
	// changed is told whenever a switch moved, so the menu that did not move
	// it is redrawn: the tray icon's, or the one in the bar, through the page.
	changed []func()
}

// LoadSettings reads the switches of this home, answering the defaults when
// there is no file yet or the one there cannot be read: a window that cannot
// read its settings opens on the defaults rather than not at all.
func LoadSettings(home string, log *slog.Logger) *SettingsStore {
	s := &SettingsStore{path: filepath.Join(home, "desktop", settingsFile), log: logger(log)}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.log.Warn("the desktop settings could not be read, so this window opens on the defaults", "file", s.path, "error", err)
		}
		return s
	}
	if err := json.Unmarshal(b, &s.cur); err != nil {
		s.log.Warn("the desktop settings are not readable JSON, so this window opens on the defaults", "file", s.path, "error", err)
		s.cur = Settings{}
	}
	return s
}

// Settings answers what the window is running on.
func (s *SettingsStore) Settings() Settings {
	if s == nil {
		return Settings{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// SetSettings writes the switches down and applies what moved.
//
// A switch that could not be applied is not written: the file says what this
// machine is actually doing, so the menu that reads it back draws the truth.
func (s *SettingsStore) SetSettings(next Settings) error {
	if s == nil {
		return errors.New("desktop: this window keeps no settings")
	}
	s.mu.Lock()
	cur := s.cur
	s.mu.Unlock()

	if next.Autostart != cur.Autostart {
		if err := setAutostart(next.Autostart); err != nil {
			return err
		}
	}
	if next.Framed != cur.Framed {
		if err := s.applyFrame(next.Framed); err != nil {
			return err
		}
	}
	if next.Tray != cur.Tray {
		if err := s.applyTray(next.Tray); err != nil {
			return err
		}
	}

	s.mu.Lock()
	s.cur = next
	watchers := append([]func(){}, s.changed...)
	s.mu.Unlock()
	err := s.write(next)
	// The machine is already doing it, so whoever draws a switch is told even
	// when the file could not be written: a menu that drew the file rather
	// than the machine would be the lie this store exists to avoid.
	for _, tell := range watchers {
		tell()
	}
	return err
}

// watch registers something to be told when a switch moves. The launcher
// registers one watcher: it redraws the tray icon's menu and tells the page to
// read the settings again, so a switch thrown in either menu moves in both.
func (s *SettingsStore) watch(fn func()) {
	if s == nil || fn == nil {
		return
	}
	s.mu.Lock()
	s.changed = append(s.changed, fn)
	s.mu.Unlock()
}

func (s *SettingsStore) write(v Settings) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("desktop: the settings folder: %w", err)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("desktop: writing the settings: %w", err)
	}
	return nil
}

// bind gives the store the window and the tray of this run.
func (s *SettingsStore) bind(win *WindowHandle, tray *trayIcon) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.win, s.tray = win, tray
	s.mu.Unlock()
}

// bindQuit gives the store the way to end this run, for the tray's last row.
func (s *SettingsStore) bindQuit(quit func()) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.quit = quit
	s.mu.Unlock()
}

// MenuRows answers the rows of the tray icon's menu: the same ones the menu in
// the tab strip draws, with showing and hiding the window above them.
func (s *SettingsStore) MenuRows() []shell.MenuItem {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	win, cur := s.win, s.cur
	s.mu.Unlock()
	return shell.MenuRows(cur, shell.MenuInTray, win.Supported())
}

// PickMenu does what choosing a row of that menu means.
//
// A switch is the same write the strip's menu makes, so the two menus cannot
// disagree about what the machine is doing. The two window rows and the new
// chat are what only the tray has to offer: the strip is inside the window and
// has the plus beside its tabs.
func (s *SettingsStore) PickMenu(id string) error {
	if s == nil {
		return errors.New("desktop: this window keeps no settings")
	}
	s.mu.Lock()
	win, cur, quit := s.win, s.cur, s.quit
	s.mu.Unlock()
	switch id {
	case shell.MenuShowWindow:
		return win.Activate()
	case shell.MenuHideWindow:
		return win.Minimise()
	case shell.MenuQuit:
		// Closing the window is what ends the run, the same path as its own
		// close button, so nothing of the session is lost. A window that
		// cannot be asked - or is not there at all - leaves the run to be
		// ended directly.
		if err := win.Close(); err == nil {
			return nil
		}
		if quit == nil {
			return errors.New("desktop: there is nothing to quit here")
		}
		quit()
		return nil
	case shell.MenuNewChat:
		// The window comes back first: a chat opened behind a hidden window is
		// one nobody asked for.
		if err := win.Activate(); err != nil {
			return err
		}
		win.RequestChord(chordNewTab)
		return nil
	}
	next, ok := shell.ApplyMenuSwitch(cur, id)
	if !ok {
		return fmt.Errorf("desktop: there is no menu row %q", id)
	}
	return s.SetSettings(next)
}

func (s *SettingsStore) applyFrame(framed bool) error {
	s.mu.Lock()
	win := s.win
	s.mu.Unlock()
	if win == nil || !win.Supported() {
		// Nothing can be asked of this window, so the frame is whatever the
		// window manager gave it. Remembering the switch is still right: the
		// next launch opens the window that way from the start.
		return nil
	}
	if framed {
		return win.Decorate()
	}
	return win.Undecorate()
}

func (s *SettingsStore) applyTray(show bool) error {
	s.mu.Lock()
	tray := s.tray
	s.mu.Unlock()
	if tray == nil {
		return errors.New("desktop: this machine has no system tray this window can reach")
	}
	if show {
		return tray.Show()
	}
	tray.Hide()
	return nil
}
