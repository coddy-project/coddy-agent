package tui

import "github.com/charmbracelet/lipgloss"

// Color palette - dark terminal aesthetic.
var (
	colorBg        = lipgloss.AdaptiveColor{Light: "#f0f0f0", Dark: "#0d0d0d"}
	colorFg        = lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#e8e8e8"}
	colorSubtle    = lipgloss.AdaptiveColor{Light: "#999999", Dark: "#555555"}
	colorAccent    = lipgloss.AdaptiveColor{Light: "#0066ff", Dark: "#4da6ff"}
	colorHighlight = lipgloss.AdaptiveColor{Light: "#cc6600", Dark: "#ff8c00"}
	colorSuccess   = lipgloss.AdaptiveColor{Light: "#006600", Dark: "#44bb44"}
	colorError     = lipgloss.AdaptiveColor{Light: "#cc0000", Dark: "#ff4444"}
	colorWarning   = lipgloss.AdaptiveColor{Light: "#cc8800", Dark: "#ffbb44"}
	colorBorder    = lipgloss.AdaptiveColor{Light: "#cccccc", Dark: "#333333"}
	colorUser      = lipgloss.AdaptiveColor{Light: "#0066ff", Dark: "#7eb8ff"}
	colorAgent     = lipgloss.AdaptiveColor{Light: "#336600", Dark: "#88cc44"}
	colorTool      = lipgloss.AdaptiveColor{Light: "#880088", Dark: "#cc88ff"}
	colorModeAgent = lipgloss.AdaptiveColor{Light: "#006600", Dark: "#44bb44"}
	colorModePlan  = lipgloss.AdaptiveColor{Light: "#cc6600", Dark: "#ff8c00"}
	// colorInputBg is the background for the user message box (slightly lighter than terminal bg).
	colorInputBg = lipgloss.AdaptiveColor{Light: "#e0e0e0", Dark: "#1e1e1e"}
)

// Styles used throughout the TUI.
var (
	styleBase = lipgloss.NewStyle().
			Background(colorBg).
			Foreground(colorFg)

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent).
			PaddingLeft(1)

	styleStatusBar = lipgloss.NewStyle().
			Foreground(colorSubtle).
			PaddingLeft(1).
			PaddingRight(1)

	styleVersion = lipgloss.NewStyle().
			Foreground(colorSubtle)

	styleModeAgent = lipgloss.NewStyle().
			Foreground(colorModeAgent).
			Bold(true)

	styleModePlan = lipgloss.NewStyle().
			Foreground(colorModePlan).
			Bold(true)

	styleInputBorder = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorBorder).
				PaddingLeft(1).
				PaddingRight(1)

	styleInputBorderFocused = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorAccent).
				PaddingLeft(1).
				PaddingRight(1)

	styleUserMessage = lipgloss.NewStyle().
				Foreground(colorUser).
				Bold(false)

	styleAgentMessage = lipgloss.NewStyle().
				Foreground(colorFg)

	styleUserLabel = lipgloss.NewStyle().
			Foreground(colorUser).
			Bold(true)

	styleAgentLabel = lipgloss.NewStyle().
			Foreground(colorAgent).
			Bold(true)

	styleToolCall = lipgloss.NewStyle().
			Foreground(colorTool).
			Italic(true)

	styleToolPending = lipgloss.NewStyle().
				Foreground(colorSubtle).
				Italic(true)

	styleToolDone = lipgloss.NewStyle().
			Foreground(colorSuccess)

	styleToolFailed = lipgloss.NewStyle().
		Foreground(colorError)

	// styleToolFocused highlights a tool entry that has keyboard focus.
	styleToolFocused = lipgloss.NewStyle().
			Foreground(colorHighlight).
			Bold(true)

	// styleToolArgs shows the command/path summary line of a tool call.
	styleToolArgs = lipgloss.NewStyle().
			Foreground(colorAccent).
			Italic(true)

	// styleToolOutput shows the tool output lines when expanded.
	styleToolOutput = lipgloss.NewStyle().
			Foreground(colorSubtle)

	styleLogoLine1 = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	styleLogoLine2 = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#0044cc", Dark: "#66aaff"})

	styleLogoLine3 = lipgloss.NewStyle().
			Foreground(colorSubtle)

	styleHint = lipgloss.NewStyle().
		Foreground(colorSubtle).
		Italic(true)

	// styleAgentFooter renders "mode · model · Xs" below agent responses.
	styleAgentFooter = lipgloss.NewStyle().
				Foreground(colorSubtle).
				Italic(true)

	styleSessionID = lipgloss.NewStyle().
			Foreground(colorSubtle)

	styleModalBg = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorAccent).
			Padding(1, 2).
			Background(colorBg)

	styleModalTitle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true).
			MarginBottom(1)

	styleModalItem = lipgloss.NewStyle().
			Foreground(colorFg).
			PaddingLeft(1)

	styleModalItemSelected = lipgloss.NewStyle().
				Foreground(colorBg).
				Background(colorAccent).
				PaddingLeft(1)

	styleModalShortcut = lipgloss.NewStyle().
				Foreground(colorSubtle)

	stylePermissionTitle = lipgloss.NewStyle().
				Foreground(colorWarning).
				Bold(true)

	styleDivider = lipgloss.NewStyle().
			Foreground(colorBorder)

	styleCurrentPathBase = lipgloss.NewStyle().
			Foreground(colorSubtle).
			Italic(true)

	// Scrollbar styles
	styleScrollTrack = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#cccccc", Dark: "#2a2a2a"})

	styleScrollThumb = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#666666", Dark: "#666666"})
)

// logo returns the multi-line ASCII art logo for coddy.
// Each letter is 4 chars wide + 1 space separator, same style as OpenCode.
//
//	C        O        D        D        Y
//	▄▀▀▀    █▀▀█    █▀▀▄    █▀▀▄    █  █
//	█       █  █    █  █    █  █     ▀▀▄
//	▀▀▀▀    ▀▀▀▀    ▀▀▀▀    ▀▀▀▀       ▀
func logo() string {
	// The accent dot sits above the logo (like OpenCode's ▄ above the O).
	accent := " "
	line1  := "  ▄▀▀▀ █▀▀█ █▀▀▄ █▀▀▄ █  █"
	line2  := "  █    █  █ █  █ █  █  ▀▀▄"
	line3  := "  ▀▀▀▀ ▀▀▀▀ ▀▀▀▀ ▀▀▀▀    ▀"

	return styleLogoLine3.Render(accent) + "\n" +
		styleLogoLine1.Render(line1) + "\n" +
		styleLogoLine2.Render(line2) + "\n" +
		styleLogoLine3.Render(line3)
}
