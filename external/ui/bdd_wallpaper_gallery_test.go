//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The server gallery is read by the browser tests, with authenticated API bytes stubbed.
func TestWebUIWallpaperGalleryFeature(t *testing.T) {
	steps := []struct{ step, file, name string }{
		{`^Appearance offers a compact always-visible gallery$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper Appearance offers a compact always-visible gallery in en"},
		{`^a selected thumbnail is marked without a duplicate preview$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper a selected thumbnail is marked without a duplicate preview"},
		{`^upload preserves an existing private background exactly and never uploads it automatically$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper upload preserves an existing private background exactly and never uploads it automatically"},
		{`^a duplicate upload selects the existing server filename and the grid is sorted$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper a duplicate upload selects the existing server filename and the grid is sorted"},
		{`^confirmed deletion of a\.png clears only a matching background$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"wallpaper deletion confirmation confirmed deletion of a.png clears only a matching background"},
		{`^confirmed deletion of z\.png clears only a matching background$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"wallpaper deletion confirmation confirmed deletion of z.png clears only a matching background"},
		{`^remote uploads, thumbnails, selections and deletes use header authentication$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"wallpaper lifecycle and failures remote uploads, thumbnails, selections and deletes use header authentication"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_wallpaper_gallery",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, s := range steps {
				file, name := s.file, s.name
				sc.Step(s.step, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_wallpaper_gallery.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI wallpaper gallery feature failed")
	}
}
