//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The gallery controls, browser-local selection and stylesheet are exercised by Vitest.
func TestWebUIWallpaperFeature(t *testing.T) {
	steps := []struct{ step, file, name string }{
		{`^upload selects the actual returned name when no background is set$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper upload selects the actual returned name when no background is set"},
		{`^clear background keeps the server file and clears only the browser selection$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper clear background keeps the server file and clears only the browser selection"},
		{`^the page carries the wallpaper and its dimming while one is set$`,
			"src/ui/theme/backdropPrefs.test.ts",
			"wallpaper <html> carries the wallpaper and its dimming while one is set"},
		{`^the wallpaper covers the viewport, dimmed toward the theme's canvas colour$`,
			"src/ui/theme/wallpaperCss.test.ts",
			"a wallpaper covers the viewport, dimmed toward the theme's canvas colour"},
		{`^a readable picture larger than 20 MiB becomes the wallpaper$`,
			"src/ui/theme/backdropPrefs.test.ts",
			"wallpaper a readable picture larger than 20 MiB becomes the wallpaper"},
		{`^a gallery thumbnail larger than 20 MiB is displayed$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper a gallery thumbnail larger than 20 MiB is displayed"},
		{`^a wallpaper remains visible in simplified mode$`,
			"src/ui/theme/wallpaperCss.test.ts",
			"a wallpaper remains visible in simplified mode"},
		{`^dimming is kept in a cookie and bounded to 0\.\.90$`,
			"src/ui/theme/backdropPrefs.test.ts",
			"wallpaper dimming is kept in a cookie and bounded to 0..90"},
		{`^Appearance offers a compact always-visible gallery$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper Appearance offers a compact always-visible gallery in en"},
		{`^the backdrop store holds only wallpaper choices$`,
			"src/ui/theme/backdropPrefs.test.ts",
			"the backdrop store holds only wallpaper choices"},
		{`^wallpaper leaves the native gradients, grain and panel glass in place without a backdrop canvas$`,
			"src/ui/theme/wallpaperCss.test.ts",
			"wallpaper leaves the native gradients, grain and panel glass in place without a backdrop canvas"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_wallpaper",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^readable pictures of any geometry can become the wallpaper$`, func() error {
				for _, geometry := range []string{"1x1", "600x1600", "100000x1", "8192x4096"} {
					name := "wallpaper a readable " + geometry + " picture becomes the wallpaper without a geometry limit"
					if err := runVitestScenario("src/ui/theme/backdropPrefs.test.ts", name); err != nil {
						return err
					}
				}
				return nil
			})
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
