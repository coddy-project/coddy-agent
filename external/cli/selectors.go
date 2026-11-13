//go:build cli

package cli

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
)

// selectorModal frames a SelectList between accent borders with a title and a
// type-to-filter input (pi selector pattern: DynamicBorder + title + list +
// help + DynamicBorder).
type selectorModal struct {
	tui.Container

	theme  *tui.Theme
	title  string
	list   *tui.SelectList
	filter string
	help   string
	// body is wrapped text between the title and the list: what the choice
	// is about, read whole before choosing (the declaration a trust
	// confirmation approves). Empty for a plain selector.
	body string

	// chrome is every child but the list. A border is one row; the title, the
	// body and the help line are as wide as the terminal and wrap, so their
	// height is known only at the width a frame is drawn at.
	chrome []tui.Component

	OnDone func(item *tui.SelectItem) // nil item = cancelled
	// onChange asks for a frame after the selection or the filter moved.
	onChange func()
	// maxVisible is the window the caller asked for; the list never grows
	// past it. rows is the terminal height and under says how many rows the
	// app draws beneath the modal, both nil or 0 until the app says.
	maxVisible int
	rows       int
	under      func(width int) int
}

func newSelectorModal(theme *tui.Theme, title string, items []tui.SelectItem, maxVisible int, requestRender func()) *selectorModal {
	m := &selectorModal{theme: theme, title: title, onChange: requestRender, maxVisible: maxVisible}
	m.list = tui.NewSelectList(items, maxVisible, selectListTheme(theme), tui.SelectListLayout{MinPrimaryColumnWidth: 12, MaxPrimaryColumnWidth: 32})
	m.help = "↑↓ navigate · enter select · esc cancel · type to filter"
	m.list.OnSelect = func(item tui.SelectItem) {
		if m.OnDone != nil {
			m.OnDone(&item)
		}
	}
	m.list.OnCancel = func() {
		if m.OnDone != nil {
			m.OnDone(nil)
		}
	}
	m.rebuild()
	return m
}

// FitTerminal tells the modal how tall the terminal is and how many rows the
// app draws under it, so a selector on a phone-sized screen keeps its title,
// its selected row and its help line visible. The app says it when the modal
// opens and again on every resize; under is asked at the width of the frame,
// because the footer takes a third line while a turn override is on.
func (m *selectorModal) FitTerminal(rows int, under func(width int) int) {
	m.rows, m.under = rows, under
}

// Render gives the list the rows the rest of the modal leaves: a selector that
// does not fit scrolls its own title off the screen, and the title is what says
// what the choice is about. The budget is counted here rather than once on open
// because the body wraps at the width of the frame and the goal menu rewrites
// it while the modal is on screen.
func (m *selectorModal) Render(width int) []string {
	visible := m.maxVisible
	if m.rows > 0 {
		visible = min(visible, m.rows-m.chromeRows(width))
	}
	m.list.SetMaxVisible(visible)
	return m.Container.Render(width)
}

// chromeRows counts every row of the terminal the list cannot have: the
// borders, the title, the body and the help line, the list's own scroll
// marker, and whatever the app draws under the modal.
func (m *selectorModal) chromeRows(width int) int {
	rows := 1
	for _, c := range m.chrome {
		rows += len(c.Render(width))
	}
	if m.under != nil {
		rows += m.under(width)
	}
	return rows
}

func (m *selectorModal) rebuild() {
	m.Clear()
	m.chrome = nil
	th := m.theme
	m.addChrome(tui.NewDynamicBorder(th.FgFn(roleBorderAccent)))
	title := th.Fg(roleAccent, th.Bold(m.title))
	if m.filter != "" {
		title += th.Fg(roleMuted, "  filter: "+m.filter)
	}
	m.addChrome(tui.NewText(title, 1, 0, nil))
	if m.body != "" {
		m.addChrome(tui.NewText(m.body, 1, 0, nil))
	}
	m.AddChild(m.list)
	m.addChrome(tui.NewText(th.Fg(roleDim, m.help), 1, 0, nil))
	m.addChrome(tui.NewDynamicBorder(th.FgFn(roleBorderAccent)))
}

// addChrome adds a child that is not a list item and counts toward the rows
// the list cannot have.
func (m *selectorModal) addChrome(c tui.Component) {
	m.chrome = append(m.chrome, c)
	m.AddChild(c)
}

// HandleInput routes navigation to the list and printable input to the filter.
func (m *selectorModal) HandleInput(data []byte) {
	s := string(data)
	if key, ok := tui.ParseKey(data); ok {
		switch key.String() {
		case "backspace", "shift+backspace":
			if m.filter != "" {
				m.filter = tui.TrimLastGrapheme(m.filter)
				m.list.SetFilter(m.filter)
				m.rebuild()
				if m.onChange != nil {
					m.onChange()
				}
			}
			return
		case "up", "down", "enter", "escape", "ctrl+c":
			m.list.HandleInput(data)
			if m.onChange != nil {
				m.onChange()
			}
			return
		}
		if key.Text != "" {
			s = key.Text
		}
	}
	if len(s) > 0 && !strings.ContainsRune(s, 0x1b) && s[0] >= 0x20 && s[0] != 0x7f {
		m.filter += tui.SanitizeText(s)
		m.list.SetFilter(m.filter)
		m.rebuild()
		if m.onChange != nil {
			m.onChange()
		}
	}
}
