//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The wallpaper is a client-side choice kept in the browser: the block of
// Settings, the store and the painter are read by the Vitest tests each step
// runs.
func TestWebUIWallpaperFeature(t *testing.T) {
	steps := []struct{ step, file, name string }{
		{`^a chosen picture shows its preview, the dimming and Remove$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper a chosen picture shows its preview, the dimming and Remove"},
		{`^the picture becomes the wallpaper and Remove takes it away$`,
			"src/ui/theme/backdropPrefs.test.ts",
			"wallpaper a picture of at least 1280x720 becomes the wallpaper"},
		{`^the page carries the wallpaper and its dimming while one is set$`,
			"src/ui/theme/backdropPrefs.test.ts",
			"wallpaper <html> carries the wallpaper and its dimming while one is set"},
		{`^the wallpaper covers the viewport, dimmed toward the theme's canvas colour$`,
			"src/ui/theme/smoothBackdropCss.test.ts",
			"a wallpaper covers the viewport, dimmed toward the theme's canvas colour"},
		{`^a picture under 1280x720 is refused and the message names its size$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper a picture under 1280x720 is refused and the message names its size"},
		{`^a file that is not a picture or is too large is refused$`,
			"src/ui/theme/backdropPrefs.test.ts",
			"wallpaper a file that is not a picture or is too large is refused"},
		{`^dimming is kept in a cookie and bounded to 0\.\.90$`,
			"src/ui/theme/backdropPrefs.test.ts",
			"wallpaper dimming is kept in a cookie and bounded to 0..90"},
		{`^each panel's image over a wallpaper is blurred the way its own glass is$`,
			"src/ui/theme/smoothBackdropCss.test.ts",
			"over a wallpaper each panel's image is blurred the way its own glass is"},
		{`^the blur keeps the tone and smooths a step$`,
			"src/ui/theme/canvasBackdrop.test.ts",
			"wallpaper filters boxBlur keeps the mean, smooths a step and leaves its input alone"},
		{`^the panel's saturation leaves grey alone$`,
			"src/ui/theme/canvasBackdrop.test.ts",
			"wallpaper filters saturateField leaves grey alone and pushes a colour away from it"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_wallpaper",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, s := range steps {
				file, name := s.file, s.name
				sc.Step(s.step, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_wallpaper.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI wallpaper feature failed")
	}
}
