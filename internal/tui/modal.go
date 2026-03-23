package tui

import (
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// modalKind identifies which modal is currently open.
type modalKind int

const (
	modalNone modalKind = iota
	modalCommands
	modalModels
	modalPermission
)

// modalState holds the current modal state.
type modalState struct {
	kind     modalKind
	query    string
	selected int

	// For permission modal.
	permParams acp.PermissionRequestParams
	permReply  chan<- *acp.PermissionResult
}

// commandEntry is a single item in the command palette.
type commandEntry struct {
	label    string
	shortcut string
}

// allCommands returns all available commands shown in the palette.
func allCommands() []commandEntry {
	return []commandEntry{
		{label: "Switch session", shortcut: "ctrl+l"},
		{label: "New session", shortcut: "ctrl+n"},
		{label: "Switch model", shortcut: "ctrl+m"},
		{label: "Switch mode", shortcut: "tab"},
		{label: "Toggle input", shortcut: "ctrl+x"},
		{label: "Hide tips", shortcut: "ctrl+h"},
		{label: "Toggle theme", shortcut: "ctrl+t"},
		{label: "Help", shortcut: ""},
		{label: "Exit the app", shortcut: ""},
	}
}

// filteredCommands returns commands matching the search query.
func filteredCommands(query string) []commandEntry {
	all := allCommands()
	if query == "" {
		return all
	}
	q := strings.ToLower(query)
	var out []commandEntry
	for _, c := range all {
		if strings.Contains(strings.ToLower(c.label), q) {
			out = append(out, c)
		}
	}
	return out
}

// renderCommandsModal renders the command palette modal.
func renderCommandsModal(query string, selected int) string {
	var b strings.Builder

	title := styleModalTitle.Render("Commands")
	b.WriteString(title + "\n\n")

	searchLine := styleModalItem.Render("Search  " + query + "█")
	b.WriteString(searchLine + "\n\n")

	cmds := filteredCommands(query)
	for i, cmd := range cmds {
		var line string
		if cmd.shortcut != "" {
			line = fmt.Sprintf("%-28s %s", cmd.label, styleModalShortcut.Render(cmd.shortcut))
		} else {
			line = cmd.label
		}
		if i == selected {
			b.WriteString(styleModalItemSelected.Render(line) + "\n")
		} else {
			b.WriteString(styleModalItem.Render(line) + "\n")
		}
	}

	b.WriteString("\n" + styleModalShortcut.Render("esc to close"))
	return styleModalBg.Render(b.String())
}

// renderModelsModal renders the model picker modal.
func renderModelsModal(modelIDs []string, currentModel string, selected int) string {
	var b strings.Builder

	title := styleModalTitle.Render("Switch model")
	b.WriteString(title + "\n\n")

	if len(modelIDs) == 0 {
		b.WriteString(styleHint.Render("No models configured") + "\n")
	} else {
		for i, id := range modelIDs {
			label := id
			if id == currentModel {
				label += "  (current)"
			}
			if i == selected {
				b.WriteString(styleModalItemSelected.Render(label) + "\n")
			} else {
				b.WriteString(styleModalItem.Render(label) + "\n")
			}
		}
	}

	b.WriteString("\n" + styleModalShortcut.Render("enter to select  esc to close"))
	return styleModalBg.Render(b.String())
}

// renderPermissionModal renders the permission request modal.
func renderPermissionModal(params acp.PermissionRequestParams, selected int) string {
	var b strings.Builder

	title := stylePermissionTitle.Render("Permission required")
	b.WriteString(title + "\n\n")

	tc := params.ToolCall
	b.WriteString(styleModalItem.Render(fmt.Sprintf("Tool:  %s", tc.Title)) + "\n")

	for _, item := range tc.Content {
		if item.Content.Text != "" {
			b.WriteString(styleModalItem.Render(item.Content.Text) + "\n")
		}
	}

	b.WriteString("\n")
	for i, opt := range params.Options {
		if i == selected {
			b.WriteString(styleModalItemSelected.Render(opt.Name) + "\n")
		} else {
			b.WriteString(styleModalItem.Render(opt.Name) + "\n")
		}
	}

	b.WriteString("\n" + styleModalShortcut.Render("enter to confirm  esc to cancel"))
	return styleModalBg.Render(b.String())
}

// centerOverlay places the overlay string centered within width x height.
// It returns a new string with the overlay merged into the base.
func centerOverlay(base, overlay string, width, height int) string {
	overlayLines := strings.Split(overlay, "\n")
	oh := len(overlayLines)
	ow := 0
	for _, l := range overlayLines {
		if w := visibleWidth(l); w > ow {
			ow = w
		}
	}

	top := (height - oh) / 2
	left := (width - ow) / 2
	if top < 0 {
		top = 0
	}
	if left < 0 {
		left = 0
	}

	baseLines := strings.Split(base, "\n")
	for len(baseLines) < height {
		baseLines = append(baseLines, "")
	}

	for i, ol := range overlayLines {
		row := top + i
		if row >= len(baseLines) {
			break
		}
		bl := baseLines[row]
		plain := stripAnsi(bl)
		runes := []rune(plain)

		for len(runes) < left+len([]rune(stripAnsi(ol))) {
			runes = append(runes, ' ')
		}

		olRunes := []rune(stripAnsi(ol))
		for j, r := range olRunes {
			if left+j < len(runes) {
				runes[left+j] = r
			}
		}
		baseLines[row] = strings.Repeat(" ", left) + string(olRunes)
		_ = runes
	}

	return strings.Join(baseLines, "\n")
}

// visibleWidth returns the display width of a string ignoring ANSI escapes.
func visibleWidth(s string) int {
	return len([]rune(stripAnsi(s)))
}

// stripAnsi removes ANSI escape sequences.
func stripAnsi(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
