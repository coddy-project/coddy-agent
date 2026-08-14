//go:build cli

package cli

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// header renders the startup block: logo line, hint line(s), and the
// [Context] / [Skills] (+ [Rules] / [MCP] when expanded) sections
// (pi interactive-mode header).
type header struct {
	tui.Container

	theme    *tui.Theme
	expanded bool

	contextFiles []string
	skillNames   []string
	rulesCount   int
	mcpNames     []string
}

func newHeader(theme *tui.Theme) *header {
	h := &header{theme: theme}
	h.rebuild()
	return h
}

// SetSections sets the resource lists shown under the hints.
func (h *header) SetSections(contextFiles, skillNames []string, rulesCount int, mcpNames []string) {
	h.contextFiles = contextFiles
	h.skillNames = skillNames
	h.rulesCount = rulesCount
	h.mcpNames = mcpNames
	h.rebuild()
}

// SetExpanded toggles the full hint list.
func (h *header) SetExpanded(expanded bool) {
	h.expanded = expanded
	h.rebuild()
}

// Expanded reports the current state.
func (h *header) Expanded() bool { return h.expanded }

var compactHints = "escape interrupt · ctrl+c/ctrl+d clear/exit · / commands · ctrl+o more"

var fullHints = []string{
	"escape to interrupt",
	"ctrl+c to clear, twice to exit",
	"ctrl+d to exit (empty)",
	"shift+tab to cycle reasoning level",
	"ctrl+p/shift+ctrl+p to cycle models",
	"ctrl+l to select model",
	"ctrl+o to expand tools",
	"ctrl+t to expand thinking",
	"/ for commands",
	"enter to send, shift+enter (ctrl+j) for newline",
	"up/down for prompt history",
}

func (h *header) rebuild() {
	h.Clear()
	th := h.theme
	h.AddChild(tui.NewSpacer(1))

	logo := th.Bold(th.Fg(roleAccent, "coddy")) + th.Fg(roleDim, " v"+version.Get())
	h.AddChild(tui.NewText(logo, 1, 0, nil))

	if h.expanded {
		var lines []string
		for _, hint := range fullHints {
			lines = append(lines, th.Fg(roleDim, hint))
		}
		h.AddChild(tui.NewText(strings.Join(lines, "\n"), 1, 0, nil))
	} else {
		h.AddChild(tui.NewText(th.Fg(roleDim, compactHints), 1, 0, nil))
	}

	if len(h.contextFiles) > 0 {
		h.AddChild(tui.NewSpacer(1))
		h.AddChild(tui.NewText(th.Fg(roleMdHeading, "[Context]"), 0, 0, nil))
		h.AddChild(tui.NewText(th.Fg(roleDim, strings.Join(sanitizeAll(h.contextFiles), "\n")), 2, 0, nil))
	}
	if len(h.skillNames) > 0 {
		h.AddChild(tui.NewSpacer(1))
		h.AddChild(tui.NewText(th.Fg(roleMdHeading, "[Skills]"), 0, 0, nil))
		h.AddChild(tui.NewText(th.Fg(roleDim, strings.Join(sanitizeAll(h.skillNames), ", ")), 2, 0, nil))
	}
	if h.expanded {
		if h.rulesCount > 0 {
			h.AddChild(tui.NewSpacer(1))
			h.AddChild(tui.NewText(th.Fg(roleMdHeading, "[Rules]"), 0, 0, nil))
			h.AddChild(tui.NewText(th.Fg(roleDim, itoa(h.rulesCount)+" project rule(s)"), 2, 0, nil))
		}
		if len(h.mcpNames) > 0 {
			h.AddChild(tui.NewSpacer(1))
			h.AddChild(tui.NewText(th.Fg(roleMdHeading, "[MCP]"), 0, 0, nil))
			h.AddChild(tui.NewText(th.Fg(roleDim, strings.Join(sanitizeAll(h.mcpNames), ", ")), 2, 0, nil))
		}
	}
}

func sanitizeAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, tui.SanitizeText(s))
	}
	return out
}
