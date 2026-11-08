//go:build desktop && linux && !android

package desktop

import (
	"errors"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
)

// What the desktop asks the menu object, answered without a message bus: the
// tree it draws from, and what picking a row means. Publishing it needs a
// session bus and a tray to register with, which no test runner has; the
// protocol arithmetic below is what can be held here.

type menuStand struct {
	mu     sync.Mutex
	rows   []shell.MenuItem
	picked []string
	err    error
}

func (s *menuStand) list() []shell.MenuItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows
}

func (s *menuStand) pick(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.picked = append(s.picked, id)
	return s.err
}

func standRows() []shell.MenuItem {
	return []shell.MenuItem{
		{ID: shell.MenuShowWindow, Kind: shell.MenuAction, Label: "Show the window"},
		{Kind: shell.MenuSeparator},
		{ID: shell.MenuTray, Kind: shell.MenuSwitch, Label: "Show the tray icon", Checked: true},
	}
}

func newMenuStand() (*trayMenu, *menuStand) {
	stand := &menuStand{rows: standRows()}
	m := newTrayMenu(stand.list, stand.pick)
	m.items = stand.list()
	return m, stand
}

func TestTheMenuNumbersItsRowsFromOne(t *testing.T) {
	m, _ := newMenuStand()
	_, layout, derr := m.GetLayout(menuRoot, -1, nil)
	if derr != nil {
		t.Fatalf("the layout: %v", derr)
	}
	if layout.ID != menuRoot {
		t.Fatalf("the root is item %d", layout.ID)
	}
	if got := layout.Props["children-display"].Value(); got != "submenu" {
		t.Fatalf("the root says children-display %v", got)
	}
	if len(layout.Children) != 3 {
		t.Fatalf("the root has %d children", len(layout.Children))
	}
	for i, child := range layout.Children {
		item, ok := child.Value().(menuLayout)
		if !ok {
			t.Fatalf("child %d is %T, not a menu item", i, child.Value())
		}
		if want := int32(i) + 1; item.ID != want {
			t.Fatalf("child %d is item %d, wanted %d", i, item.ID, want)
		}
	}
}

func TestASwitchCarriesItsTickAndASeparatorItsType(t *testing.T) {
	m, _ := newMenuStand()
	props := propsOf(standRows()[2])
	if props["toggle-type"].Value() != "checkmark" {
		t.Fatalf("a switch is drawn as %v", props["toggle-type"].Value())
	}
	if props["toggle-state"].Value() != int32(1) {
		t.Fatalf("a switch that is on reports %v", props["toggle-state"].Value())
	}
	off := propsOf(shell.MenuItem{ID: shell.MenuTray, Kind: shell.MenuSwitch})
	if off["toggle-state"].Value() != int32(0) {
		t.Fatalf("a switch that is off reports %v", off["toggle-state"].Value())
	}
	if propsOf(standRows()[1])["type"].Value() != "separator" {
		t.Fatal("a separator is not drawn as one")
	}
	// And the same through the call a host makes about several items at once.
	got, derr := m.GetGroupProperties([]int32{2, 3, 99}, nil)
	if derr != nil {
		t.Fatalf("the properties: %v", derr)
	}
	if len(got) != 2 {
		t.Fatalf("it answered for %d items, and one of the three does not exist", len(got))
	}
}

func TestPickingARowIsTheRowsOwnId(t *testing.T) {
	m, stand := newMenuStand()
	if derr := m.Event(3, "clicked", dbus.MakeVariant(""), 0); derr != nil {
		t.Fatalf("picking the switch: %v", derr)
	}
	if len(stand.picked) != 1 || stand.picked[0] != shell.MenuTray {
		t.Fatalf("the launcher was told %v", stand.picked)
	}
}

// A separator, an id of no row and an event that is not a click are all
// nothing to act on: a menu must never do something nobody chose.
func TestNothingButAClickOnARowIsActedOn(t *testing.T) {
	m, stand := newMenuStand()
	_ = m.Event(2, "clicked", dbus.MakeVariant(""), 0) // the separator
	_ = m.Event(99, "clicked", dbus.MakeVariant(""), 0)
	_ = m.Event(0, "clicked", dbus.MakeVariant(""), 0)
	_ = m.Event(3, "hovered", dbus.MakeVariant(""), 0)
	_ = m.Event(3, "opened", dbus.MakeVariant(""), 0)
	if len(stand.picked) != 0 {
		t.Fatalf("the launcher was told %v", stand.picked)
	}
}

// A switch the machine would not throw keeps its tick where the machine left
// it, and the desktop is told so rather than left drawing a lie.
func TestASwitchThatDidNotTakeIsReportedBack(t *testing.T) {
	m, stand := newMenuStand()
	stand.err = errors.New("this session has no tray")
	derr := m.Event(3, "clicked", dbus.MakeVariant(""), 0)
	if derr == nil {
		t.Fatal("the desktop was told the switch took")
	}
}

// The rows are retaken before the menu is drawn, so a tick that moved while
// the menu was closed is drawn where it now is.
func TestTheMenuIsRetakenBeforeItIsShown(t *testing.T) {
	m, stand := newMenuStand()
	_, before := m.current()
	stand.mu.Lock()
	stand.rows = []shell.MenuItem{{ID: shell.MenuTray, Kind: shell.MenuSwitch, Label: "Show the tray icon"}}
	stand.mu.Unlock()
	needs, derr := m.AboutToShow(menuRoot)
	if derr != nil {
		t.Fatalf("about to show: %v", derr)
	}
	if !needs {
		t.Fatal("the host was told the menu it has is still current")
	}
	rows, after := m.current()
	if after <= before {
		t.Fatalf("the revision stayed at %d", after)
	}
	if len(rows) != 1 || rows[0].Checked {
		t.Fatalf("the menu still draws %+v", rows)
	}
}
