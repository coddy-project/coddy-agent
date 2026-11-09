//go:build desktop

package desktop_test

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/desktop"
	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
)

type blockingVisibleTray struct {
	visibleStarted chan struct{}
	allowVisible   chan struct{}
	events         *desktop.MenuWindow
}

func newBlockingVisibleTray(events *desktop.MenuWindow) *blockingVisibleTray {
	return &blockingVisibleTray{
		visibleStarted: make(chan struct{}),
		allowVisible:   make(chan struct{}),
		events:         events,
	}
}

func (t *blockingVisibleTray) Show() error { return nil }
func (t *blockingVisibleTray) Hide()       { t.events.RecordEvent("hide") }
func (t *blockingVisibleTray) Visible() bool {
	close(t.visibleStarted)
	<-t.allowVisible
	return true
}

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

func TestDisablingTrayRestoresAMinimisedWindow(t *testing.T) {
	win := desktop.NewMenuWindow()
	handle := desktop.NewWindowHandle()
	handle.Attach(win)
	settings := desktop.LoadSettings(t.TempDir(), nil)
	settings.BindWindowAndTray(handle, desktop.NewTrayPresence(true))
	handle.AttachSettings(settings)
	if err := settings.SetSettings(desktop.Settings{Tray: true}); err != nil {
		t.Fatalf("enabling the tray: %v", err)
	}
	if err := handle.RequestClose(); err != nil {
		t.Fatalf("closing to the tray: %v", err)
	}
	if win.Hidden() != 1 {
		t.Fatalf("the window was minimised %d times", win.Hidden())
	}

	if err := settings.SetSettings(desktop.Settings{Tray: false}); err != nil {
		t.Fatalf("disabling the tray: %v", err)
	}
	if win.Shown() != 1 {
		t.Fatalf("disabling the tray restored the window %d times", win.Shown())
	}
}

func TestDisablingTrayCannotPassBetweenTheVisibilityCheckAndMinimise(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)

	win := desktop.NewMenuWindow()
	handle := desktop.NewWindowHandle()
	handle.Attach(win)
	settings := desktop.LoadSettings(t.TempDir(), nil)
	tray := newBlockingVisibleTray(win)
	settings.BindWindowAndTray(handle, tray)
	handle.AttachSettings(settings)
	if err := settings.SetSettings(desktop.Settings{Tray: true}); err != nil {
		t.Fatalf("enabling the tray: %v", err)
	}

	closed := make(chan error, 1)
	go func() { closed <- handle.RequestClose() }()
	<-tray.visibleStarted

	disabled := make(chan error, 1)
	disableStarted := make(chan struct{})
	go func() {
		close(disableStarted)
		disabled <- settings.SetSettings(desktop.Settings{Tray: false})
	}()
	<-disableStarted
	// With one scheduler thread, the disable goroutine now runs until it
	// either finishes or reaches the contested operation. There is no timer
	// whose expiry could hide a broken interleaving.
	runtime.Gosched()
	close(tray.allowVisible)
	if err := <-closed; err != nil {
		t.Fatalf("closing to the tray: %v", err)
	}
	if err := <-disabled; err != nil {
		t.Fatalf("disabling the tray: %v", err)
	}
	if win.Hidden() != 1 || win.Shown() != 1 {
		t.Fatalf("minimised=%d restored=%d, want one of each in that order", win.Hidden(), win.Shown())
	}
	want := []string{"minimise", "activate", "hide"}
	if got := win.Events(); !reflect.DeepEqual(got, want) {
		t.Fatalf("operations happened as %v, want %v", got, want)
	}
}
