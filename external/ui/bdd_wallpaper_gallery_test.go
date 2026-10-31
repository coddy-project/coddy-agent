//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The server gallery is read by the browser tests, with authenticated API bytes stubbed.
func TestWebUIWallpaperGalleryFeature(t *testing.T) {
	steps := []struct{ step, file, name string }{
		{`^nothing is requested from the server gallery until it is opened$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper → Gallery nothing is requested from the server gallery until it is opened"},
		{`^a picked server picture becomes the browser wallpaper without credits$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper → Gallery a picked server picture becomes the browser wallpaper without credits"},
		{`^remote thumbnails and selections carry authentication only in headers$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper → Gallery remote thumbnails and selections carry authentication only in headers"},
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
