//go:build desktop && linux && !android

package desktop

import (
	"io"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

type closedBusTransport struct {
	io.Reader
	io.Writer
}

func (closedBusTransport) Close() error { return nil }

type closeRecordingWindow struct {
	noWindowControl
	minimised int
	closed    int
}

func (w *closeRecordingWindow) Supported() bool { return true }
func (w *closeRecordingWindow) Minimise() error { w.minimised++; return nil }
func (w *closeRecordingWindow) Close() error    { w.closed++; return nil }

func TestCloseRequestWithADeadTrayConnectionCloses(t *testing.T) {
	conn, err := dbus.NewConn(closedBusTransport{Reader: strings.NewReader(""), Writer: io.Discard})
	if err != nil {
		t.Fatalf("creating a private connection: %v", err)
	}
	tray := &trayIcon{conn: conn}
	win := &closeRecordingWindow{}
	handle := NewWindowHandle()
	handle.Attach(win)
	settings := LoadSettings(t.TempDir(), nil)
	settings.bind(handle, tray)
	handle.AttachSettings(settings)
	settings.cur.Tray = true

	if err := conn.Close(); err != nil {
		t.Fatalf("closing the connection: %v", err)
	}
	if conn.Connected() {
		t.Fatal("the test connection is still connected")
	}
	if err := handle.RequestClose(); err != nil {
		t.Fatalf("closing with a dead tray connection: %v", err)
	}
	if win.closed != 1 || win.minimised != 0 {
		t.Fatalf("closed=%d minimised=%d, want one close and no minimise", win.closed, win.minimised)
	}
}
