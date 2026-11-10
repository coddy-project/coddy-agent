//go:build cli

package cli

import (
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

func TestOverlaySearchEncodedInput(t *testing.T) {
	for _, surface := range []string{"selector", "docs"} {
		for _, tc := range []struct{ name, input, want string }{
			{"UTF-8", "Ж", "seed🙂Ж"},
			{"associated text", "\x1b[0;;101:769u", "seed🙂e\u0301"},
			{"shift backspace", "\x1b[27;2;127~", "seed"},
			{"repeat backspace", "\x1b[127;2:2u", "seed"},
			{"release backspace", "\x1b[127;2:3u", "seed🙂"},
			{"release letter", "\x1b[97;2:3u", "seed🙂"},
			{"control shortcut", "\x1b[97;6u", "seed🙂"},
			{"alt shortcut", "\x1b[97;3u", "seed🙂"},
			{"invalid text", "\x1b[97;2;27u", "seed🙂"},
		} {
			t.Run(surface+"/"+tc.name, func(t *testing.T) {
				var input tui.InputHandler
				var text func() string
				if surface == "selector" {
					m := newSelectorModal(newTheme("dark"), "Models", nil, 5, nil)
					input, text = m, func() string { return m.filter }
				} else {
					m, _ := newTestDocsModal(t)
					input, text = m, func() string { return m.query }
				}
				input.HandleInput([]byte("seed🙂"))
				input.HandleInput([]byte(tc.input))
				if got := text(); got != tc.want {
					t.Fatalf("search %q, want %q", got, tc.want)
				}
			})
		}
	}
}

func TestOverlayLetterCommandsIgnoreShortcutsAndReleases(t *testing.T) {
	for _, seq := range []string{
		"\x1b[115;5u", "\x1b[114;3u", "\x1b[115;2:3u", "\x1b[114;2:3u",
		"\x1b[110;5u", "\x1b[110;2:3u",
	} {
		t.Run(seq, func(t *testing.T) {
			called := false
			tasks := newTasksModal(newTheme("dark"), nil)
			tasks.SetRows([]bgtask.Snapshot{taskRow("bg_shift", bgtask.StatusRunning, time.Minute)})
			tasks.OnStop = func(string) { called = true }
			tasks.OnRefresh = func() { called = true }
			tasks.HandleInput([]byte(seq))
			if called {
				t.Fatal("shortcut or release ran a task command")
			}
			help, _ := newTestDocsModal(t)
			page, _ := help.lib.Page("guide/one")
			help.OpenPage(page, "")
			help.HandleInput([]byte(seq))
			if help.PageTitle() != "First page" {
				t.Fatal("shortcut or release turned the page")
			}
		})
	}
}

func TestDocsEncodedPageCommands(t *testing.T) {
	m, _ := newTestDocsModal(t)
	page, _ := m.lib.Page("guide/one")
	m.OpenPage(page, "")
	m.Render(80)
	m.HandleInput([]byte("\x1b[32;2u"))
	if m.offset == 0 {
		t.Fatal("encoded space must scroll the page")
	}
	m.HandleInput([]byte("\x1b[27;2;78~"))
	m.HandleInput([]byte("\x1b[112:80;2u"))
	if m.PageTitle() != "First page" {
		t.Fatalf("encoded P must return to the first page, got %q", m.PageTitle())
	}
	m.HandleInput([]byte("\x1b[47u"))
	if m.page != nil {
		t.Fatal("encoded / must return to search")
	}
}
