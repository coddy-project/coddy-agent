//go:build desktop

package shell

// The window's menu, in one place.
//
// It is drawn twice - by the button at the left of the tab strip, and by the
// tray icon, where the desktop's own menu is what a right click opens - and
// the two have to offer the same thing. Deciding the rows here, in Go, is what
// makes that true by construction: the page asks for them over
// GET /desktop/menu and draws what it is given, and the tray builds its DBus
// menu from the same call. A row added below appears in both without either
// side being told about it.
//
// The tray's menu carries two rows the bar's cannot: showing the window and
// hiding it. A bar that is inside the window has nothing to say about either.

// The kinds of row. A switch keeps the menu open and moves its tick only once
// the launcher says the change took; an action does its thing and the menu
// closes behind it.
const (
	MenuAction    = "action"
	MenuSwitch    = "switch"
	MenuSeparator = "separator"
)

// The rows, by id. Every switch is named after the field of Settings it moves,
// so a menu can send what the operator picked without a table of its own.
const (
	MenuNewChat        = "new-chat"
	MenuShowWindow     = "show-window"
	MenuHideWindow     = "hide-window"
	MenuQuit           = "quit"
	MenuAutostart      = "autostart"
	MenuTray           = "tray"
	MenuStartMinimised = "start_minimised"
	MenuFramed         = "framed"
)

// MenuPlace is which of the two menus is being drawn.
type MenuPlace string

const (
	// MenuInBar is the button at the left of the tab strip.
	MenuInBar MenuPlace = "bar"
	// MenuInTray is what a right click on the tray icon opens.
	MenuInTray MenuPlace = "tray"
)

// MenuItem is one row.
type MenuItem struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Checked is the tick of a switch, and nothing on any other kind.
	Checked bool `json:"checked,omitempty"`
}

// MenuRows answers the rows of one menu.
//
// driven says whether this machine's window can be asked to do anything (an
// X11 session can, a Wayland one cannot). Where it cannot, the frame switch is
// left out - the window manager's frame is the only one there is - and so are
// the tray's two window rows, because nothing would answer them.
func MenuRows(s Settings, place MenuPlace, driven bool) []MenuItem {
	var rows []MenuItem
	if place == MenuInTray && driven {
		rows = append(rows,
			MenuItem{ID: MenuShowWindow, Kind: MenuAction, Label: "Show the window"},
			MenuItem{ID: MenuHideWindow, Kind: MenuAction, Label: "Hide the window"},
			MenuItem{Kind: MenuSeparator},
		)
	}
	rows = append(rows,
		MenuItem{ID: MenuNewChat, Kind: MenuAction, Label: "New chat"},
		MenuItem{ID: MenuAutostart, Kind: MenuSwitch, Label: "Open at login", Checked: s.Autostart},
		MenuItem{ID: MenuTray, Kind: MenuSwitch, Label: "Show the tray icon", Checked: s.Tray},
		MenuItem{ID: MenuStartMinimised, Kind: MenuSwitch, Label: "Start minimised", Checked: s.StartMinimised},
	)
	if driven {
		rows = append(rows, MenuItem{ID: MenuFramed, Kind: MenuSwitch, Label: "Show the system frame", Checked: s.Framed})
	}
	// The tray's menu ends with the way out of the application, set apart from
	// the switches above it. The bar's does not: it is drawn inside the window,
	// an arm's length from the window's own close button.
	if place == MenuInTray {
		rows = append(rows,
			MenuItem{Kind: MenuSeparator},
			MenuItem{ID: MenuQuit, Kind: MenuAction, Label: "Quit Coddy"},
		)
	}
	return rows
}

// ApplyMenuSwitch answers the settings that picking a switch asks for: the
// current ones with that one field flipped. A row that is not a switch leaves
// them as they are and says so.
func ApplyMenuSwitch(s Settings, id string) (Settings, bool) {
	switch id {
	case MenuAutostart:
		s.Autostart = !s.Autostart
	case MenuTray:
		s.Tray = !s.Tray
	case MenuStartMinimised:
		s.StartMinimised = !s.StartMinimised
	case MenuFramed:
		s.Framed = !s.Framed
	default:
		return s, false
	}
	return s, true
}
