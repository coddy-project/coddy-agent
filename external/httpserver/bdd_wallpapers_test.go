//go:build http

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/cucumber/godog"
)

func TestWallpapersHTTPFeature(t *testing.T) {
	var s *Server
	var picture []byte
	var names []string
	var response *httptest.ResponseRecorder
	var uploadedName string
	var home string
	var cfg *config.Config
	var mgr *session.Manager
	var cancel context.CancelFunc
	var done chan error
	start := func() error {
		ctx, stop := context.WithCancel(context.Background())
		cancel = stop
		done = make(chan error, 1)
		ready := make(chan *Server, 1)
		go func() {
			done <- Serve(ctx, Options{Cfg: cfg, Mgr: mgr, Log: slog.Default(), DefaultCWD: home, ListenAddr: "127.0.0.1:0", OnServer: func(server *Server) {
				if server != nil {
					ready <- server
				}
			}})
		}()
		select {
		case s = <-ready:
			return nil
		case err := <-done:
			cancel = nil
			return fmt.Errorf("start server: %v", err)
		case <-time.After(10 * time.Second):
			stop()
			return fmt.Errorf("server startup timed out")
		}
	}
	suite := godog.TestSuite{
		Name:    "wallpapers_http",
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/wallpapers_http.feature"}, TestingT: t, Strict: true},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				if cancel != nil {
					cancel()
					cancel = nil
					return ctx, <-done
				}
				return ctx, nil
			})
			sc.Step(`^a fresh wallpaper installation$`, func() error {
				home = t.TempDir()
				cfg = &config.Config{}
				cfg.Paths.Home = home
				runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
					return "", nil
				}
				mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
				return nil
			})
			sc.Step(`^the wallpaper server starts$`, start)
			sc.Step(`^the gallery contains the four bundled pictures$`, func() error {
				w := wallpaperRequest(s, "GET", "/coddy/wallpapers", "")
				if w.Code != 200 || !containsDefaultNames(w.Body.String()) {
					return fmt.Errorf("gallery: %d %s", w.Code, w.Body.String())
				}
				return nil
			})
			sc.Step(`^I delete the bundled picture "([^"]+)" and restart the server$`, func(name string) error {
				response = wallpaperBodyRequest(s, "DELETE", name, nil, "")
				if response.Code != 204 {
					return fmt.Errorf("delete: %d %s", response.Code, response.Body.String())
				}
				uploadedName = name
				cancel()
				cancel = nil
				if err := <-done; err != nil {
					return err
				}
				return start()
			})
			sc.Step(`^the deleted bundled picture remains absent$`, func() error {
				w := wallpaperBodyRequest(s, "GET", uploadedName, nil, "")
				if w.Code != 404 {
					return fmt.Errorf("deleted picture: %d", w.Code)
				}
				return nil
			})
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
			sc.Step(`^an authenticated wallpaper server and a picture on my computer$`, func() error {
				s, _ = wallpaperTestServer(t)
				picture = wallpaperTestPictureDimensions(t, t.TempDir(), "source.png", 1, 1)
				return nil
			})
			sc.Step(`^I upload that picture as "([^"]+)"$`, func(name string) error {
				response = wallpaperBodyRequest(s, "POST", name, bytes.NewReader(picture), "wallpaper-test-token")
				return nil
			})
			sc.Step(`^the upload (creates|reuses) "([^"]+)"$`, func(action, name string) error {
				status, created := 200, false
				if action == "creates" {
					status, created = 201, true
				}
				want := fmt.Sprintf(`{"wallpaper":{"name":%q},"created":%t}`, name, created)
				if response.Code != status || !bytes.Equal(bytes.TrimSpace(response.Body.Bytes()), []byte(want)) {
					return fmt.Errorf("upload: %d %s, want %s", response.Code, response.Body.String(), want)
				}
				uploadedName = name
				return nil
			})
			sc.Step(`^the gallery contains only "([^"]+)"$`, func(name string) error {
				if len(names) != 1 || names[0] != name {
					return fmt.Errorf("gallery: %v", names)
				}
				return nil
			})
			sc.Step(`^I request the uploaded wallpaper$`, func() error {
				response = wallpaperBodyRequest(s, "GET", uploadedName, nil, "wallpaper-test-token")
				return nil
			})
			sc.Step(`^I delete the uploaded wallpaper$`, func() error {
				response = wallpaperBodyRequest(s, "DELETE", uploadedName, nil, "wallpaper-test-token")
				if response.Code != 204 || response.Body.Len() != 0 {
					return fmt.Errorf("delete: %d %s", response.Code, response.Body.String())
				}
				return nil
			})
			sc.Step(`^the gallery is empty$`, func() error {
				response = wallpaperRequest(s, "GET", "/coddy/wallpapers", "wallpaper-test-token")
				if response.Code != 200 || !bytes.Equal(bytes.TrimSpace(response.Body.Bytes()), []byte(`{"wallpapers":[]}`)) {
					return fmt.Errorf("gallery: %d %s", response.Code, response.Body.String())
				}
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
