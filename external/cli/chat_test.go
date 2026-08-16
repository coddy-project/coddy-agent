//go:build cli

package cli

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
)

// Transcript blocks separate with one leading blank row (pi spacing): a user
// box must not sit flush against the header, and assistant text must not sit
// flush against the tool box above it.

func TestUserMessageStartsWithASeparatorRow(t *testing.T) {
	theme := newTheme("dark")
	lines := newUserMessage(theme, "hello").Render(40)
	if len(lines) < 2 {
		t.Fatalf("unexpected render: %q", lines)
	}
	if visible := tui.StripTerminalSequences(lines[0]); strings.TrimSpace(visible) != "" {
		t.Fatalf("first row must be a blank separator, got %q", lines[0])
	}
	if strings.Contains(lines[0], "48;") {
		t.Fatalf("the separator row must not carry the message background: %q", lines[0])
	}
}

func TestAssistantMessageStartsWithASeparatorRow(t *testing.T) {
	theme := newTheme("dark")
	msg := newAssistantMessage(theme, markdownTheme(theme, false), false)
	msg.AppendText("the answer")
	lines := msg.Render(40)
	if len(lines) < 2 {
		t.Fatalf("unexpected render: %q", lines)
	}
	if visible := tui.StripTerminalSequences(lines[0]); strings.TrimSpace(visible) != "" {
		t.Fatalf("first row must be a blank separator, got %q", lines[0])
	}
}
