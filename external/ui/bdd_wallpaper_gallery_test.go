//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The gallery lives in the browser: the block of Settings, the list of photos
// and the store are read by the Vitest tests each step runs, with the network
// stubbed.
func TestWebUIWallpaperGalleryFeature(t *testing.T) {
	steps := []struct{ step, file, name string }{
		{`^nothing is requested from the gallery until it is opened$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper → Gallery nothing is requested from the gallery until it is opened"},
		{`^every gallery photo names its author and its Unsplash page$`,
			"src/ui/theme/wallpaperGallery.test.ts",
			"every gallery photo names its author and its Unsplash page"},
		{`^a picked photo becomes the wallpaper with its author's credit$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper → Gallery a picked photo becomes the wallpaper with its author's credit"},
		{`^the photo is asked for at the screen's size and never under the minimum$`,
			"src/ui/theme/wallpaperGallery.test.ts",
			"a gallery photo is asked for at the screen's size, 16:9, never under the minimum"},
		{`^the author stays with the photo until it is removed$`,
			"src/ui/theme/backdropPrefs.test.ts",
			"wallpaper a gallery photo keeps its author with it, and Remove forgets both"},
		{`^a photo that does not load says so$`,
			"src/ui/theme/BackdropSettings.test.tsx",
			"Settings → Appearance → Wallpaper → Gallery a photo that does not load says so"},
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
