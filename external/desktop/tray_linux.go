//go:build desktop && linux && !android

package desktop

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

// The tray icon is a StatusNotifierItem on the session bus, not a window
// docked into a tray.
//
// That is the protocol every current desktop speaks - KDE's own, and GNOME's
// through its extension - and the icon travels over the bus as pixels, so
// nothing here draws into an X window, loads a theme or depends on the
// XEmbed bridge a Wayland session no longer runs. The item is one bus name,
// one exported object and one call to the watcher; taking the icon away is
// closing the connection, which is what the host watches for.

// The mark, once: the tray icon, and the icon the window carries for a task
// bar that has no installed entry to take one from (window_icon_x11.go). It
// is the same file the Linux packages install for the menu entry.
//
//go:embed assets/coddy-icon.png
var iconPNG []byte

// The same mark at the size a task bar actually draws. _NET_WM_ICON carries
// several sizes and the task bar picks; a window given only the large one
// makes every panel scale it down itself.
//
//go:embed assets/coddy-icon-48.png
var iconSmallPNG []byte

const (
	sniInterface  = "org.kde.StatusNotifierItem"
	sniPath       = "/StatusNotifierItem"
	watcherName   = "org.kde.StatusNotifierWatcher"
	watcherPath   = "/StatusNotifierWatcher"
	watcherMethod = "org.kde.StatusNotifierWatcher.RegisterStatusNotifierItem"
)

// trayPixmap is one size of the icon on the wire: a(iiay), the pixels ARGB32
// in network byte order.
type trayPixmap struct {
	Width  int32
	Height int32
	Pixels []byte
}

// trayTooltip is (sa(iiay)ss): an icon, a title and a description.
type trayTooltip struct {
	IconName    string
	IconPixmap  []trayPixmap
	Title       string
	Description string
}

// trayIcon is Coddy's mark in the system tray while the window runs.
type trayIcon struct {
	log *slog.Logger
	// activate is what a click on the icon does: bring the window back.
	activate func()
	// menu is what a right click opens: the same rows as the menu in the tab
	// strip, with showing and hiding the window above them.
	menu *trayMenu

	mu   sync.Mutex
	conn *dbus.Conn
}

func newTrayIcon(log *slog.Logger, activate func(), menu *trayMenu) *trayIcon {
	return &trayIcon{log: logger(log), activate: activate, menu: menu}
}

// Show publishes the item and registers it with the tray of this session.
func (t *trayIcon) Show() error {
	if t == nil {
		return errors.New("desktop: there is no tray icon to show")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.conn != nil {
		return nil
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("desktop: this session has no message bus for a tray icon: %w", err)
	}
	if err := t.publish(conn); err != nil {
		_ = conn.Close()
		return err
	}
	t.conn = conn
	return nil
}

// menuChanged redraws the icon's menu, for a switch thrown somewhere else -
// the menu in the tab strip, or the window's own settings.
func (t *trayIcon) menuChanged() {
	if t == nil || t.menu == nil {
		return
	}
	t.menu.changed()
}

// publish is everything the item needs to exist: a name of its own, the
// object behind it, and the watcher being told about it.
func (t *trayIcon) publish(conn *dbus.Conn) error {
	// The name is the one the specification prescribes, so a host that lists
	// the bus rather than the watcher finds the item too.
	name := fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid())
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("desktop: the tray icon could not take its name on the bus: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return errors.New("desktop: another program already holds this tray icon's name on the bus")
	}

	pixmap, err := trayPixmapOf(iconPNG)
	if err != nil {
		return err
	}
	if err := conn.Export(&sniObject{tray: t}, sniPath, sniInterface); err != nil {
		return fmt.Errorf("desktop: exporting the tray icon: %w", err)
	}
	// An item that names no menu object is one the desktop opens nothing for,
	// so the path is published first and the property below points at it.
	menuAt := dbus.ObjectPath("/")
	if t.menu != nil {
		if menuAt, err = t.menu.publish(conn); err != nil {
			return err
		}
	}
	props, err := prop.Export(conn, sniPath, map[string]map[string]*prop.Prop{
		sniInterface: {
			// ApplicationStatus is what an ordinary program's icon is; a host
			// sorts the tray by category.
			"Category":   {Value: "ApplicationStatus", Writable: false, Emit: prop.EmitTrue},
			"Id":         {Value: "coddy-desktop", Writable: false, Emit: prop.EmitTrue},
			"Title":      {Value: "Coddy", Writable: false, Emit: prop.EmitTrue},
			"Status":     {Value: "Active", Writable: false, Emit: prop.EmitTrue},
			"IconName":   {Value: "", Writable: false, Emit: prop.EmitTrue},
			"IconPixmap": {Value: []trayPixmap{pixmap}, Writable: false, Emit: prop.EmitTrue},
			"ToolTip": {Value: trayTooltip{
				IconPixmap:  []trayPixmap{pixmap},
				Title:       "Coddy",
				Description: "Coddy agent in a window",
			}, Writable: false, Emit: prop.EmitTrue},
			// The item is not itself a menu: a left click activates it and the
			// window comes back, a right click opens the menu below.
			"ItemIsMenu": {Value: false, Writable: false, Emit: prop.EmitTrue},
			"Menu":       {Value: menuAt, Writable: false, Emit: prop.EmitTrue},
		},
	})
	if err != nil {
		return fmt.Errorf("desktop: publishing the tray icon's properties: %w", err)
	}
	node := &introspect.Node{
		Name: sniPath,
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			prop.IntrospectData,
			{
				Name:       sniInterface,
				Methods:    introspect.Methods(&sniObject{}),
				Properties: props.Introspection(sniInterface),
			},
		},
	}
	if err := conn.Export(introspect.NewIntrospectable(node), sniPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return fmt.Errorf("desktop: exporting the tray icon's introspection: %w", err)
	}
	watcher := conn.Object(watcherName, watcherPath)
	if call := watcher.Call(watcherMethod, 0, name); call.Err != nil {
		return fmt.Errorf("desktop: this session has no system tray to put the icon in: %w", call.Err)
	}
	t.log.Debug("the tray icon is up", "name", name)
	return nil
}

// Hide takes the icon away. The host is watching the bus name, so dropping the
// connection is what removes the item - there is no "unregister" in the
// protocol.
func (t *trayIcon) Hide() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.conn == nil {
		return
	}
	_ = t.conn.Close()
	t.conn = nil
}

// Visible reports whether this run still owns the published tray connection.
// A close request reads it before minimising, so a vanished tray never leaves
// a hidden window with no icon to bring it back.
func (t *trayIcon) Visible() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.conn != nil
}

// sniObject is the item's methods: what a host calls when somebody clicks the
// icon. A click brings the window back, which is what a tray icon is for; the
// menu is drawn by the host itself off the object the Menu property names, so
// ContextMenu is only reached by a host that ignores it, and bringing the
// window back is the least surprising thing to do there.
type sniObject struct{ tray *trayIcon }

func (o *sniObject) Activate(_, _ int32) *dbus.Error          { o.tray.activated(); return nil }
func (o *sniObject) SecondaryActivate(_, _ int32) *dbus.Error { o.tray.activated(); return nil }
func (o *sniObject) ContextMenu(_, _ int32) *dbus.Error       { o.tray.activated(); return nil }
func (o *sniObject) Scroll(_ int32, _ string) *dbus.Error     { return nil }

func (t *trayIcon) activated() {
	if t == nil || t.activate == nil {
		return
	}
	t.activate()
}

// trayPixmapOf turns the embedded icon into what the bus carries: ARGB32 in
// network byte order, one byte per channel, alpha first.
//
// The colour is read back as non-premultiplied, which is the form the hosts
// expect; Go's own image.RGBA is premultiplied, so the conversion is explicit
// rather than a cast.
func trayPixmapOf(b []byte) (trayPixmap, error) {
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return trayPixmap{}, fmt.Errorf("desktop: the tray icon is not a readable PNG: %w", err)
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	pixels := make([]byte, 0, w*h*4)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c, _ := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			pixels = append(pixels, c.A, c.R, c.G, c.B)
		}
	}
	// G115: w and h are the bounds of an icon embedded in this binary, 128
	// pixels square, and the protocol carries them as signed 32-bit numbers.
	return trayPixmap{Width: int32(w), Height: int32(h), Pixels: pixels}, nil //nolint:gosec // G115: see above.
}
