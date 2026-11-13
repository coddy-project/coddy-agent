//go:build cli

package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

func TestConsoleShiftInputFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "console-shift-input",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var editor *Editor
			feed := func(input string) {
				buffer := NewStdinBuffer(time.Second)
				buffer.Emit = editor.HandleInput
				// A tty read may split even an escape sequence in two.
				for _, b := range []byte(input) {
					buffer.Feed([]byte{b})
				}
			}
			sc.Step(`^an empty console editor receiving terminal input$`, func() {
				editor = newTestEditor()
			})
			sc.Step(`^the terminal sends "([^"]*)" characters while Shift stays held$`, func(protocol string) error {
				var input string
				switch protocol {
				case "modifyOtherKeys":
					input = "A\x1b[27;2;66~\x1b[27;2;33~\x1b[27;2;32~\x1b[27;2;67~"
				case "Kitty":
					input = "A\x1b[98:66;2u\x1b[49:33;2u\x1b[32;2u\x1b[99:67;2u"
				default:
					return fmt.Errorf("unknown keyboard protocol %q", protocol)
				}
				feed(input)
				return nil
			})
			sc.Step(`^the terminal corrects a letter using "([^"]*)" while Shift stays held$`, func(protocol string) error {
				switch protocol {
				case "modifyOtherKeys":
					feed("hello \x1b[27;2;88~\x1b[27;2;127~\x1b[27;2;66~")
				case "Kitty":
					feed("hello \x1b[120:88;2u\x1b[127;2u\x1b[98:66;2u")
				default:
					return fmt.Errorf("unknown keyboard protocol %q", protocol)
				}
				return nil
			})
			sc.Step(`^the console draft is "([^"]*)"$`, func(want string) error {
				if got := editor.Text(); got != want {
					return fmt.Errorf("draft %q, want %q", got, want)
				}
				return nil
			})
		},
		Options: &godog.Options{
			Format: "pretty", Paths: []string{"../../../features/cli_shift_input.feature"},
			TestingT: t, Strict: true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("console Shift input feature failed")
	}
}
