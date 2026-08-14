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

	OnDone   func(item *tui.SelectItem) // nil item = cancelled
	onChange func()
}

func newSelectorModal(theme *tui.Theme, title string, items []tui.SelectItem, maxVisible int, requestRender func()) *selectorModal {
	m := &selectorModal{theme: theme, title: title, onChange: requestRender}
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

func (m *selectorModal) rebuild() {
	m.Clear()
	th := m.theme
	m.AddChild(tui.NewDynamicBorder(th.FgFn(roleBorderAccent)))
	title := th.Fg(roleAccent, th.Bold(m.title))
	if m.filter != "" {
		title += th.Fg(roleMuted, "  filter: "+m.filter)
	}
	m.AddChild(tui.NewText(title, 1, 0, nil))
	m.AddChild(m.list)
	m.AddChild(tui.NewText(th.Fg(roleDim, m.help), 1, 0, nil))
	m.AddChild(tui.NewDynamicBorder(th.FgFn(roleBorderAccent)))
}

// HandleInput routes navigation to the list and printable input to the filter.
func (m *selectorModal) HandleInput(data []byte) {
	if key, ok := tui.ParseKey(data); ok {
		switch key.String() {
		case "backspace":
			if m.filter != "" {
				runes := []rune(m.filter)
				m.filter = string(runes[:len(runes)-1])
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
	}
	s := string(data)
	if len(s) > 0 && !strings.ContainsRune(s, 0x1b) && s[0] >= 0x20 && s[0] != 0x7f {
		m.filter += tui.SanitizeText(s)
		m.list.SetFilter(m.filter)
		m.rebuild()
		if m.onChange != nil {
			m.onChange()
		}
	}
}
