//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The simplified mode is a cookie, an attribute on <html> and one partial at the
// end of the stylesheet, so each step runs the Vitest test that reads them. What
// the mode does to a real page in every theme, jsdom cannot see: npm run
// check:simple walks the computed styles in a browser.
func TestWebUISimpleModeFeature(t *testing.T) {
	const setting = "src/ui/theme/SimpleModeSetting.test.tsx"
	const css = "src/ui/theme/simpleModeCss.test.ts"
	steps := []struct{ step, file, name string }{
		{`^the simplified mode switch stands between the theme grid and the language$`, setting,
			"the switch stands between the theme grid and the language"},
		{`^the name of the mode is the switch's label and what it turns off is in the tooltip$`, setting,
			"the name of the mode is the switch's label, and what it turns off is in the tooltip"},
		{`^turning it on marks the page and the cookie, and turning it off clears both$`, setting,
			"turning the switch on marks the page and the cookie, and turning it off clears both"},
		{`^a theme picked while the mode is on leaves it on$`, setting,
			"a theme picked while the mode is on leaves it on"},
		{`^a page loaded with the cookie starts in the mode$`, setting,
			"a page loaded with the cookie starts in the mode, before the first paint too"},
		{`^the simplified mode comes last in the cascade and clears shadows, blur, masks and motion$`, css,
			"the partial comes last in the cascade and clears shadows, blur, masks and motion"},
		{`^the panels of the simplified mode are solid colours of the theme$`, css,
			"the panels are solid colours of the theme"},
		{`^every gradient of the stylesheet is cleared by the simplified mode or draws a mark$`, css,
			"every gradient of the stylesheet is cleared by the mode or draws a mark"},
		{`^a jump down the transcript goes at once in the simplified mode$`, setting,
			"motion counts as reduced in the mode"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_simple_mode",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, s := range steps {
				file, name := s.file, s.name
				sc.Step(s.step, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_simple_mode.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI simplified mode feature failed")
	}
}
