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
	m.snapshot()
	return m, stand
}

func TestTheMenuHandsEveryRowAnIdOfItsOwn(t *testing.T) {
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
	seen := map[int32]bool{}
	for i, child := range layout.Children {
		item, ok := child.Value().(menuLayout)
		if !ok {
			t.Fatalf("child %d is %T, not a menu item", i, child.Value())
		}
		if item.ID == menuRoot {
			t.Fatalf("child %d took the root's id", i)
		}
		if seen[item.ID] {
			t.Fatalf("child %d shares item %d with a row above it", i, item.ID)
		}
		seen[item.ID] = true
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
	got, derr := m.GetGroupProperties([]int32{separatorID(t, m), itemOf(t, m, "Show the tray icon"), 9999}, nil)
	if derr != nil {
		t.Fatalf("the properties: %v", derr)
	}
	if len(got) != 2 {
		t.Fatalf("it answered for %d items, and one of the three does not exist", len(got))
	}
}

// itemOf answers the id the menu handed the row with that label.
func itemOf(t *testing.T, m *trayMenu, label string) int32 {
	t.Helper()
	id, ok := idsByLabel(t, m)[label]
	if !ok {
		t.Fatalf("the menu has no row labelled %q", label)
	}
	return id
}

// separatorID answers the id of the menu's first separator, which carries no
// label to be found by.
func separatorID(t *testing.T, m *trayMenu) int32 {
	t.Helper()
	_, layout, derr := m.GetLayout(menuRoot, -1, nil)
	if derr != nil {
		t.Fatalf("the layout: %v", derr)
	}
	for _, child := range layout.Children {
		item, ok := child.Value().(menuLayout)
		if ok && item.Props["type"].Value() == "separator" {
			return item.ID
		}
	}
	t.Fatal("the menu draws no separator")
	return 0
}

// Every property a host can hold on to is sent on every row, at its default
// where the row does not use it.
//
// This is not tidiness. A dbusmenu host caches an item's properties by id and
// merges what it is told next into what it has: Plasma drew the separator of
// this menu as a ticked row labelled after a switch, because the separator
// sent only its type and the label and the tick of the row that had held that
// id survived underneath it. A row that names every property it has cannot be
// drawn with another row's.
func TestEveryRowCarriesEveryPropertyAHostCanCache(t *testing.T) {
	want := []string{"label", "type", "toggle-type", "toggle-state", "enabled", "visible"}
	for _, row := range []shell.MenuItem{
		{ID: shell.MenuNewChat, Kind: shell.MenuAction, Label: "New chat"},
		{ID: shell.MenuTray, Kind: shell.MenuSwitch, Label: "Show the tray icon", Checked: true},
		{Kind: shell.MenuSeparator},
	} {
		props := propsOf(row)
		for _, name := range want {
			if _, ok := props[name]; !ok {
				t.Fatalf("the %s row %q sends no %s", row.Kind, row.ID, name)
			}
		}
	}
	action := propsOf(shell.MenuItem{ID: shell.MenuNewChat, Kind: shell.MenuAction, Label: "New chat"})
	if action["toggle-type"].Value() != "" {
		t.Fatalf("an action is drawn as %v", action["toggle-type"].Value())
	}
	if action["toggle-state"].Value() != int32(-1) {
		t.Fatalf("an action reports the tick %v", action["toggle-state"].Value())
	}
	if action["type"].Value() != "standard" {
		t.Fatalf("an action is of type %v", action["type"].Value())
	}
	sep := propsOf(shell.MenuItem{Kind: shell.MenuSeparator})
	if sep["label"].Value() != "" {
		t.Fatalf("a separator carries the label %v", sep["label"].Value())
	}
	if sep["toggle-type"].Value() != "" || sep["toggle-state"].Value() != int32(-1) {
		t.Fatalf("a separator carries a tick: %v %v", sep["toggle-type"].Value(), sep["toggle-state"].Value())
	}
}

// idsByLabel reads the menu the way a host does and answers which item is which.
func idsByLabel(t *testing.T, m *trayMenu) map[string]int32 {
	t.Helper()
	_, layout, derr := m.GetLayout(menuRoot, -1, nil)
	if derr != nil {
		t.Fatalf("the layout: %v", derr)
	}
	out := map[string]int32{}
	for _, child := range layout.Children {
		item, ok := child.Value().(menuLayout)
		if !ok {
			t.Fatalf("a child is %T, not a menu item", child.Value())
		}
		label, _ := item.Props["label"].Value().(string)
		if label != "" {
			out[label] = item.ID
		}
	}
	return out
}

// A row keeps its id when the menu around it changes.
//
// The ids used to be positions, so a row that gained a neighbour above it took
// over the id of the row that had been there - and a host that had cached that
// id drew the old row's label and tick on the new one. An id that belongs to
// the row itself cannot be inherited.
func TestARowKeepsItsIdWhenTheMenuAboveItChanges(t *testing.T) {
	m, stand := newMenuStand()
	m.snapshot()
	before := idsByLabel(t, m)["Show the tray icon"]
	if before == 0 {
		t.Fatal("the switch has no id of its own")
	}
	stand.mu.Lock()
	stand.rows = append([]shell.MenuItem{
		{ID: shell.MenuNewChat, Kind: shell.MenuAction, Label: "New chat"},
		{Kind: shell.MenuSeparator},
	}, standRows()...)
	stand.mu.Unlock()
	m.snapshot()
	after := idsByLabel(t, m)["Show the tray icon"]
	if after != before {
		t.Fatalf("the switch moved from item %d to item %d", before, after)
	}
	// And picking that id still picks that row, not whatever now stands where
	// the row used to be.
	if derr := m.Event(after, "clicked", dbus.MakeVariant(""), 0); derr != nil {
		t.Fatalf("picking the switch: %v", derr)
	}
	if len(stand.picked) != 1 || stand.picked[0] != shell.MenuTray {
		t.Fatalf("the launcher was told %v", stand.picked)
	}
}

func TestPickingARowIsTheRowsOwnId(t *testing.T) {
	m, stand := newMenuStand()
	if derr := m.Event(itemOf(t, m, "Show the tray icon"), "clicked", dbus.MakeVariant(""), 0); derr != nil {
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
	sw := itemOf(t, m, "Show the tray icon")
	_ = m.Event(separatorID(t, m), "clicked", dbus.MakeVariant(""), 0)
	_ = m.Event(9999, "clicked", dbus.MakeVariant(""), 0)
	_ = m.Event(menuRoot, "clicked", dbus.MakeVariant(""), 0)
	_ = m.Event(sw, "hovered", dbus.MakeVariant(""), 0)
	_ = m.Event(sw, "opened", dbus.MakeVariant(""), 0)
	if len(stand.picked) != 0 {
		t.Fatalf("the launcher was told %v", stand.picked)
	}
}

// A switch the machine would not throw keeps its tick where the machine left
// it, and the desktop is told so rather than left drawing a lie.
func TestASwitchThatDidNotTakeIsReportedBack(t *testing.T) {
	m, stand := newMenuStand()
	stand.err = errors.New("this session has no tray")
	derr := m.Event(itemOf(t, m, "Show the tray icon"), "clicked", dbus.MakeVariant(""), 0)
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
