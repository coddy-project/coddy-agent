//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// Smooth gradients are a client-side choice: the switch, the painter and the
// rules of the stylesheet are read by the Vitest tests each step runs.
func TestWebUISmoothGradientsFeature(t *testing.T) {
	steps := []struct{ step, file, name string }{
		{`^Settings → Appearance shows the smooth gradients switch under the notifications, off$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Gradients the smooth gradients switch stands under the notifications, off"},
		{`^turning it on is remembered in this browser$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Gradients turning it on is remembered in this browser"},
		{`^every theme spells the backdrop tokens in a form the painter reads$`,
			"src/ui/theme/canvasBackdrop.test.ts",
			"every theme spells the four backdrop tokens in a form the painter reads"},
		{`^the painter places the glows where the html background rule does$`,
			"src/ui/theme/canvasBackdrop.test.ts",
			"the painter's glow geometry matches the html background rule"},
		{`^each glow has its full alpha at its centre and none at its end stop$`,
			"src/ui/theme/canvasBackdrop.test.ts",
			"paintBackdrop puts each glow's full alpha at its centre and none at its end stop"},
		{`^every pixel carries a dither of about one level$`,
			"src/ui/theme/canvasBackdrop.test.ts",
			"paintBackdrop adds a dither of at most about one level"},
		{`^each panel shows its painted image over its plain tint$`,
			"src/ui/theme/smoothBackdropCss.test.ts",
			"in smooth mode each panel shows its painted image over its plain tint"},
		{`^the panels drop their blur and soft shadows, and the composer its vignette$`,
			"src/ui/theme/smoothBackdropCss.test.ts",
			"in smooth mode the panels drop their blur and soft shadows, and the composer its vignette"},
		{`^without a canvas nothing is marked and the panels keep their own glass$`,
			"src/ui/theme/backdropPrefs.test.ts",
			"smooth gradients <html> is marked only while the canvas runs"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_smooth_gradients",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, s := range steps {
				file, name := s.file, s.name
				sc.Step(s.step, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_smooth_gradients.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI smooth gradients feature failed")
	}
}
