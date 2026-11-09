//go:build desktop

package desktop_test

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/desktop"
	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
)

func TestCloseRequestWithAnUnavailableTrayCloses(t *testing.T) {
	win := desktop.NewMenuWindow()
	handle := desktop.NewWindowHandle()
	handle.Attach(win)
	settings := desktop.LoadSettings(t.TempDir(), nil)
	tray := desktop.NewTrayPresence(false)
	settings.BindWindowAndTray(handle, tray)
	handle.AttachSettings(settings)
	if err := settings.SetSettings(desktop.Settings{Tray: true}); err != nil {
		t.Fatalf("enabling the tray: %v", err)
	}
	// A connection can disappear after the setting was applied. The saved
	// preference must not leave the application running without a way back.
	tray.Hide()

	if err := handle.RequestClose(); err != nil {
		t.Fatalf("closing without a visible tray: %v", err)
	}
	if win.Closed() != 1 || win.Hidden() != 0 {
		t.Fatalf("closed=%d minimised=%d, want one close and no minimise", win.Closed(), win.Hidden())
	}
}

func TestQuitWithAVisibleTrayCloses(t *testing.T) {
	win := desktop.NewMenuWindow()
	handle := desktop.NewWindowHandle()
	handle.Attach(win)
	settings := desktop.LoadSettings(t.TempDir(), nil)
	settings.BindWindowAndTray(handle, desktop.NewTrayPresence(true))
	handle.AttachSettings(settings)
	if err := settings.SetSettings(desktop.Settings{Tray: true}); err != nil {
		t.Fatalf("enabling the tray: %v", err)
	}

	if err := settings.PickMenu(shell.MenuQuit); err != nil {
		t.Fatalf("quitting with a visible tray: %v", err)
	}
	if win.Closed() != 1 || win.Hidden() != 0 {
		t.Fatalf("closed=%d minimised=%d, want one close and no minimise", win.Closed(), win.Hidden())
	}
}
