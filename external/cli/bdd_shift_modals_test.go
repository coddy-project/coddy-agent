//go:build cli

package cli

import (
	"fmt"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

func TestConsoleShiftModalsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "console-shift-modals",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var surface string
			var input tui.InputHandler
			var selector *selectorModal
			var help *docsModal
			var refreshed int
			var stopped string
			feed := func(s string) {
				buffer := tui.NewStdinBuffer(time.Second)
				buffer.Emit = input.HandleInput
				for _, b := range []byte(s) {
					buffer.Feed([]byte{b})
				}
			}
			sc.Step(`^the console "([^"]*)" overlay$`, func(name string) error {
				surface = name
				switch name {
				case "selector":
					selector = newSelectorModal(newTheme("dark"), "Models", []tui.SelectItem{{Value: "Ж!", Label: "Ж!"}}, 5, nil)
					input = selector
				case "docs search", "docs page":
					help, _ = newTestDocsModal(t)
					if name == "docs page" {
						page, _ := help.lib.Page("guide/one")
						help.OpenPage(page, "")
					}
					input = help
				case "tasks":
					m := newTasksModal(newTheme("dark"), nil)
					m.SetRows([]bgtask.Snapshot{taskRow("bg_shift", bgtask.StatusRunning, time.Minute)})
					m.OnRefresh = func() { refreshed++ }
					m.OnStop = func(id string) { stopped = id }
					input = m
				default:
					return fmt.Errorf("unknown overlay %q", name)
				}
				return nil
			})
			sc.Step(`^encoded "([^"]*)" input types a letter, corrects it and adds punctuation$`, func(protocol string) error {
				switch protocol {
				case "modifyOtherKeys":
					feed("\x1b[27;2;1046~\x1b[27;2;88~\x1b[27;2;127~\x1b[27;2;33~")
				case "Kitty":
					feed("\x1b[1078;2u\x1b[120:88;2u\x1b[127;2u\x1b[49:33;2u")
				default:
					return fmt.Errorf("unknown protocol %q", protocol)
				}
				return nil
			})
			sc.Step(`^the overlay search text is "([^"]*)"$`, func(want string) error {
				got := ""
				if selector != nil {
					got = selector.filter
					if item := selector.list.SelectedItem(); item == nil || item.Value != want {
						return fmt.Errorf("filter did not select %q", want)
					}
				} else {
					got = help.query
				}
				if got != want {
					return fmt.Errorf("search %q, want %q", got, want)
				}
				return nil
			})
			sc.Step(`^encoded "([^"]*)" input runs the overlay's letter commands$`, func(protocol string) error {
				letters := "RS"
				if surface == "docs page" {
					letters = "N"
				}
				for _, r := range letters {
					switch protocol {
					case "modifyOtherKeys":
						feed(fmt.Sprintf("\x1b[27;2;%d~", r))
					case "Kitty":
						feed(fmt.Sprintf("\x1b[%d:%d;2u", r+'a'-'A', r))
					default:
						return fmt.Errorf("unknown protocol %q", protocol)
					}
				}
				return nil
			})
			sc.Step(`^the overlay commands have taken effect$`, func() error {
				if surface == "tasks" {
					if refreshed != 1 || stopped != "bg_shift" {
						return fmt.Errorf("refreshes %d, stopped %q", refreshed, stopped)
					}
				} else if help.PageTitle() != "Second page" {
					return fmt.Errorf("page %q, want Second page", help.PageTitle())
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/cli_shift_modals.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("console Shift overlays feature failed")
	}
}
