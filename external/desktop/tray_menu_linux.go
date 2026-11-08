//go:build desktop && linux && !android

package desktop

import (
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
)

// The menu behind the tray icon.
//
// A StatusNotifierItem does not draw its own menu: it names an object that
// speaks com.canonical.dbusmenu, and the desktop - Plasma's tray, GNOME's
// extension, the tray of a lighter panel - draws the menu itself from what
// that object reports. So this file is a small dbusmenu server over the rows
// of shell.MenuRows, which is also what the menu in the tab strip draws. One
// list, two menus.
//
// The protocol is a tree of numbered items, which is why the rows are indexed:
// item 0 is the root, and the row at index i is item i+1. An id therefore
// means nothing beyond one snapshot, and the snapshot is retaken whenever the
// host is about to show the menu, which is also when the ticks of the switches
// have to be current.

const (
	menuInterface = "com.canonical.dbusmenu"
	menuPath      = dbus.ObjectPath("/MenuBar")
	// menuRoot is the id of the item everything hangs off.
	menuRoot int32 = 0
)

// menuLayout is dbusmenu's (ia{sv}av): an id, the item's properties, and its
// children, each wrapped in a variant because the type is recursive.
type menuLayout struct {
	ID       int32
	Props    map[string]dbus.Variant
	Children []dbus.Variant
}

// menuProps is (ia{sv}): one item's properties, for the calls that ask about
// several items at once.
type menuProps struct {
	ID    int32
	Props map[string]dbus.Variant
}

// menuEvent is (isvu): what EventGroup carries.
type menuEvent struct {
	ID        int32
	EventID   string
	Data      dbus.Variant
	Timestamp uint32
}

// trayMenu answers the desktop's questions about the icon's menu.
type trayMenu struct {
	// rows is where the menu comes from, asked again every time the host is
	// about to draw it, and pick is what choosing a row means. Both belong to
	// the launcher; nothing here knows what a row does.
	rows func() []shell.MenuItem
	pick func(id string) error

	mu       sync.Mutex
	conn     *dbus.Conn
	revision uint32
	items    []shell.MenuItem
}

func newTrayMenu(rows func() []shell.MenuItem, pick func(string) error) *trayMenu {
	return &trayMenu{rows: rows, pick: pick, revision: 1}
}

// publish exports the menu object beside the item, and answers the path the
// item's Menu property points at.
func (m *trayMenu) publish(conn *dbus.Conn) (dbus.ObjectPath, error) {
	m.mu.Lock()
	m.conn = conn
	m.items = m.rows()
	m.mu.Unlock()

	if err := conn.Export(m, menuPath, menuInterface); err != nil {
		return "", fmt.Errorf("desktop: exporting the tray icon's menu: %w", err)
	}
	props, err := prop.Export(conn, menuPath, map[string]map[string]*prop.Prop{
		menuInterface: {
			"Version":       {Value: uint32(3), Writable: false, Emit: prop.EmitTrue},
			"TextDirection": {Value: "ltr", Writable: false, Emit: prop.EmitTrue},
			"Status":        {Value: "normal", Writable: false, Emit: prop.EmitTrue},
			"IconThemePath": {Value: []string{}, Writable: false, Emit: prop.EmitTrue},
		},
	})
	if err != nil {
		return "", fmt.Errorf("desktop: publishing the tray menu's properties: %w", err)
	}
	node := &introspect.Node{
		Name: string(menuPath),
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			prop.IntrospectData,
			{
				Name:       menuInterface,
				Methods:    introspect.Methods(m),
				Properties: props.Introspection(menuInterface),
			},
		},
	}
	if err := conn.Export(introspect.NewIntrospectable(node), menuPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return "", fmt.Errorf("desktop: exporting the tray menu's introspection: %w", err)
	}
	return menuPath, nil
}

// snapshot retakes the rows and answers them with the revision they belong to.
func (m *trayMenu) snapshot() ([]shell.MenuItem, uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = m.rows()
	m.revision++
	return m.items, m.revision
}

// current answers the rows the ids in flight were handed out against.
func (m *trayMenu) current() ([]shell.MenuItem, uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.items, m.revision
}

// changed tells the desktop that the menu it may be drawing has moved, so a
// tick thrown from the menu itself is redrawn where it was thrown.
func (m *trayMenu) changed() {
	_, revision := m.snapshot()
	m.mu.Lock()
	conn := m.conn
	m.mu.Unlock()
	if conn == nil {
		return
	}
	_ = conn.Emit(menuPath, menuInterface+".LayoutUpdated", revision, menuRoot)
}

// propsOf is one row as dbusmenu properties. A property at its default - an
// enabled, visible, standard item - is left out, which is what the protocol
// expects.
func propsOf(row shell.MenuItem) map[string]dbus.Variant {
	if row.Kind == shell.MenuSeparator {
		return map[string]dbus.Variant{"type": dbus.MakeVariant("separator")}
	}
	props := map[string]dbus.Variant{"label": dbus.MakeVariant(row.Label)}
	if row.Kind == shell.MenuSwitch {
		props["toggle-type"] = dbus.MakeVariant("checkmark")
		state := int32(0)
		if row.Checked {
			state = 1
		}
		props["toggle-state"] = dbus.MakeVariant(state)
	}
	return props
}

// GetLayout answers the tree. Only two levels exist - the root and its rows -
// so the recursion depth is honoured by answering no children below the root.
func (m *trayMenu) GetLayout(parentID, _ int32, _ []string) (uint32, menuLayout, *dbus.Error) {
	rows, revision := m.current()
	if parentID != menuRoot {
		i := int(parentID) - 1
		if i < 0 || i >= len(rows) {
			return revision, menuLayout{}, dbus.MakeFailedError(fmt.Errorf("no menu item %d", parentID))
		}
		return revision, menuLayout{ID: parentID, Props: propsOf(rows[i])}, nil
	}
	children := make([]dbus.Variant, 0, len(rows))
	for i, row := range rows {
		children = append(children, dbus.MakeVariant(menuLayout{ID: int32(i) + 1, Props: propsOf(row)}))
	}
	return revision, menuLayout{
		ID:       menuRoot,
		Props:    map[string]dbus.Variant{"children-display": dbus.MakeVariant("submenu")},
		Children: children,
	}, nil
}

func (m *trayMenu) GetGroupProperties(ids []int32, _ []string) ([]menuProps, *dbus.Error) {
	rows, _ := m.current()
	out := make([]menuProps, 0, len(ids))
	for _, id := range ids {
		i := int(id) - 1
		if i < 0 || i >= len(rows) {
			continue
		}
		out = append(out, menuProps{ID: id, Props: propsOf(rows[i])})
	}
	return out, nil
}

func (m *trayMenu) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	rows, _ := m.current()
	i := int(id) - 1
	if i < 0 || i >= len(rows) {
		return dbus.Variant{}, dbus.MakeFailedError(fmt.Errorf("no menu item %d", id))
	}
	v, ok := propsOf(rows[i])[name]
	if !ok {
		return dbus.Variant{}, dbus.MakeFailedError(fmt.Errorf("menu item %d has no %s", id, name))
	}
	return v, nil
}

// Event is a row being picked. Anything but a click - a hover, the menu
// opening - is nothing to act on here.
func (m *trayMenu) Event(id int32, eventID string, _ dbus.Variant, _ uint32) *dbus.Error {
	if eventID != "clicked" {
		return nil
	}
	rows, _ := m.current()
	i := int(id) - 1
	if i < 0 || i >= len(rows) || rows[i].Kind == shell.MenuSeparator {
		return nil
	}
	if m.pick != nil {
		if err := m.pick(rows[i].ID); err != nil {
			// The row stays where the machine actually left it, and the
			// desktop is told the menu moved so it redraws from the truth.
			m.changed()
			return dbus.MakeFailedError(err)
		}
	}
	m.changed()
	return nil
}

func (m *trayMenu) EventGroup(events []menuEvent) ([]int32, *dbus.Error) {
	for _, e := range events {
		_ = m.Event(e.ID, e.EventID, e.Data, e.Timestamp)
	}
	return nil, nil
}

// AboutToShow retakes the rows, so the ticks of the switches are what the
// machine is doing at the moment the menu opens, and says the layout moved so
// the host reads it again before drawing.
func (m *trayMenu) AboutToShow(_ int32) (bool, *dbus.Error) {
	m.snapshot()
	return true, nil
}

func (m *trayMenu) AboutToShowGroup(ids []int32) ([]int32, []int32, *dbus.Error) {
	m.snapshot()
	return ids, nil, nil
}
