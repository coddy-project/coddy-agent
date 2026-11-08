//go:build desktop

package shell

import "testing"

// The rows of the menu are decided once and drawn twice, so what each place
// gets - and what a machine that cannot be asked about its window gets - is
// worth holding here rather than in either drawing.

func idsOf(rows []MenuItem) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out
}

func TestTheBarsMenuSaysNothingAboutTheWindowItIsIn(t *testing.T) {
	for _, id := range idsOf(MenuRows(Settings{}, MenuInBar, true)) {
		if id == MenuShowWindow || id == MenuHideWindow {
			t.Fatalf("the bar offers %q, which only the tray can", id)
		}
	}
}

func TestTheTraysMenuOpensAndHidesTheWindowAboveTheRest(t *testing.T) {
	rows := MenuRows(Settings{}, MenuInTray, true)
	if len(rows) < 3 {
		t.Fatalf("the tray's menu has %d rows", len(rows))
	}
	if rows[0].ID != MenuShowWindow || rows[1].ID != MenuHideWindow {
		t.Fatalf("it starts with %q and %q", rows[0].ID, rows[1].ID)
	}
	if rows[2].Kind != MenuSeparator {
		t.Fatalf("the third row is %q, not a separator", rows[2].Kind)
	}
}

// A window nothing can be asked of - a Wayland session - keeps the frame its
// manager gave it, and there is nothing for the tray's two rows to act on
// either.
func TestAWindowThatCannotBeAskedOffersNeitherTheFrameNorTheWindowRows(t *testing.T) {
	for _, place := range []MenuPlace{MenuInBar, MenuInTray} {
		for _, id := range idsOf(MenuRows(Settings{}, place, false)) {
			switch id {
			case MenuFramed, MenuShowWindow, MenuHideWindow:
				t.Fatalf("the %s menu offers %q on a window it cannot ask", place, id)
			}
		}
	}
}

func TestEverySwitchDrawsWhatTheMachineIsDoing(t *testing.T) {
	on := Settings{Framed: true, Tray: true, Autostart: true, StartMinimised: true}
	for _, row := range MenuRows(on, MenuInTray, true) {
		if row.Kind == MenuSwitch && !row.Checked {
			t.Fatalf("%q is on and the menu draws it unticked", row.ID)
		}
	}
	for _, row := range MenuRows(Settings{}, MenuInTray, true) {
		if row.Kind == MenuSwitch && row.Checked {
			t.Fatalf("%q is off and the menu draws it ticked", row.ID)
		}
	}
}

func TestPickingASwitchFlipsExactlyThatOne(t *testing.T) {
	for _, tc := range []struct {
		id   string
		read func(Settings) bool
	}{
		{MenuAutostart, func(s Settings) bool { return s.Autostart }},
		{MenuTray, func(s Settings) bool { return s.Tray }},
		{MenuStartMinimised, func(s Settings) bool { return s.StartMinimised }},
		{MenuFramed, func(s Settings) bool { return s.Framed }},
	} {
		next, ok := ApplyMenuSwitch(Settings{}, tc.id)
		if !ok {
			t.Fatalf("%q is a switch and was not taken for one", tc.id)
		}
		if !tc.read(next) {
			t.Fatalf("picking %q did not turn it on", tc.id)
		}
		back, _ := ApplyMenuSwitch(next, tc.id)
		if back != (Settings{}) {
			t.Fatalf("picking %q twice left %+v", tc.id, back)
		}
	}
}

// An action and anything the menu does not know are not switches, and leave
// the settings exactly as they were.
func TestPickingSomethingThatIsNotASwitchChangesNothing(t *testing.T) {
	for _, id := range []string{MenuNewChat, MenuShowWindow, MenuHideWindow, "", "nonsense"} {
		next, ok := ApplyMenuSwitch(Settings{Tray: true}, id)
		if ok {
			t.Fatalf("%q was taken for a switch", id)
		}
		if next != (Settings{Tray: true}) {
			t.Fatalf("%q changed the settings to %+v", id, next)
		}
	}
}
