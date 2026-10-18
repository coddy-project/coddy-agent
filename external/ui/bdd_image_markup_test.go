//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The markup editor is React behaviour over a canvas, so each step runs the
// Vitest test that drives it in jsdom with a recording 2D context
// (src/ui/markup/markupFakes.ts); the console's editor has its own spec,
// features/cli_image_paste.feature.
func TestImageMarkupFeature(t *testing.T) {
	const composer = "src/ui/chat/Composer.test.tsx"
	const dialog = "src/ui/markup/ImageMarkupDialog.test.tsx"
	steps := map[string][2]string{
		`^a pasted picture opens in the markup editor and Done attaches it$`: {composer,
			"a pasted picture opens in the markup editor and Done attaches it"},
		`^Cancel in the markup editor attaches nothing$`: {composer,
			"Cancel in the markup editor attaches nothing"},
		`^the pencil on an attached picture marks it up in place$`: {composer,
			"the pencil on an attached picture marks it up in place"},
		`^the editor draws with the chosen tool and colour and hands back a PNG on Done$`: {dialog,
			"ImageMarkupDialog draws with the chosen tool and colour, undoes, and hands back a PNG on Done"},
		`^the editor types a label in any script and commits it with Enter$`: {dialog,
			"ImageMarkupDialog types a label in any script and commits it with Enter"},
		`^the editor crops and turns the picture$`: {dialog,
			"ImageMarkupDialog crops and turns the picture"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_image_markup",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for expr, target := range steps {
				file, name := target[0], target[1]
				sc.Step(expr, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_image_markup.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("image markup feature failed")
	}
}
