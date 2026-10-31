//go:build http

package httpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cucumber/godog"
)

func TestWallpapersHTTPFeature(t *testing.T) {
	var s *Server
	var picture []byte
	var names []string
	var response *httptest.ResponseRecorder
	suite := godog.TestSuite{
		Name:    "wallpapers_http",
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/wallpapers_http.feature"}, TestingT: t, Strict: true},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^an authenticated wallpaper server with two local pictures$`, func() error {
				var home string
				s, home = wallpaperTestServer(t)
				picture = wallpaperTestPicture(t, home, "Night sky.png")
				wallpaperTestPicture(t, home, "Alps.png")
				return nil
			})
			sc.Step(`^an authenticated wallpaper server with two local pictures of (\d+) by (\d+) pixels$`, func(width, height int) error {
				var home string
				s, home = wallpaperTestServer(t)
				picture = wallpaperTestPictureDimensions(t, home, "Night sky.png", width, height)
				wallpaperTestPictureDimensions(t, home, "Alps.png", width, height)
				return nil
			})
			sc.Step(`^I request the local wallpaper gallery$`, func() error {
				w := wallpaperRequest(s, "GET", "/coddy/wallpapers", "wallpaper-test-token")
				if w.Code != 200 {
					return fmt.Errorf("gallery status %d: %s", w.Code, w.Body.String())
				}
				var body struct {
					Wallpapers []struct {
						Name string `json:"name"`
					} `json:"wallpapers"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					return err
				}
				names = nil
				for _, row := range body.Wallpapers {
					names = append(names, row.Name)
				}
				return nil
			})
			sc.Step(`^the gallery lists the two picture names in order$`, func() error {
				if len(names) != 2 || names[0] != "Alps.png" || names[1] != "Night sky.png" {
					return fmt.Errorf("names: %v", names)
				}
				return nil
			})
			sc.Step(`^I request a listed wallpaper$`, func() error {
				response = wallpaperRequest(s, "GET", "/coddy/wallpapers/"+url.PathEscape(names[1]), "wallpaper-test-token")
				return nil
			})
			sc.Step(`^I receive that picture's original raster bytes$`, func() error {
				if response.Code != 200 || response.Header().Get("Content-Type") != "image/png" {
					return fmt.Errorf("picture status %d, headers %v", response.Code, response.Header())
				}
				if !bytes.Equal(response.Body.Bytes(), picture) {
					return fmt.Errorf("picture bytes differ")
				}
				return nil
			})
		},
	}
	if suite.Run() != 0 {
		t.Fatal("wallpapers feature failed")
	}
}
