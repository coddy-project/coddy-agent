//go:build http && ui

package ui

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// webManifest is the part of the web app manifest the browser needs to offer
// the install.
type webManifest struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
	StartURL  string `json:"start_url"`
	Scope     string `json:"scope"`
	Display   string `json:"display"`
	Icons     []struct {
		Src     string `json:"src"`
		Sizes   string `json:"sizes"`
		Type    string `json:"type"`
		Purpose string `json:"purpose"`
	} `json:"icons"`
}

// The installable app and its notifications (issue #508). The server's part -
// the manifest, the icons and the service worker - is read from the handler
// both surfaces mount; the browser's part runs as the Vitest tests that drive
// it against stubbed browser APIs.
func TestWebUIInstallableAppFeature(t *testing.T) {
	var srv *httptest.Server
	get := func(path string) (*http.Response, []byte, error) {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			return nil, nil, err
		}
		defer func() { _ = res.Body.Close() }()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, nil, err
		}
		if res.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("GET %s: status %d", path, res.StatusCode)
		}
		return res, body, nil
	}
	readManifest := func() (webManifest, error) {
		var m webManifest
		res, body, err := get("/manifest.webmanifest")
		if err != nil {
			return m, err
		}
		if ct := res.Header.Get("Content-Type"); ct != "application/manifest+json" {
			return m, fmt.Errorf("manifest Content-Type %q", ct)
		}
		if err := json.Unmarshal(body, &m); err != nil {
			return m, fmt.Errorf("manifest is not JSON: %w", err)
		}
		return m, nil
	}

	vitest := []struct{ step, file, name string }{
		{`^turning the switch on asks the browser and stays on when allowed$`,
			"src/ui/pwa/NotificationsSetting.test.tsx",
			"Settings → Appearance → Notifications turning the switch on asks the browser and stays on when allowed"},
		{`^a refusal leaves the switch off and says where to allow notifications$`,
			"src/ui/pwa/NotificationsSetting.test.tsx",
			"Settings → Appearance → Notifications a refusal leaves it off and says where to allow them"},
		{`^a hidden tab says the turn of its chat ended, and a click opens the chat$`,
			"src/ui/App.notifications.test.tsx",
			"notifications from the app a hidden tab says the turn of its chat ended, and a click opens the chat"},
		{`^a turn of a chat the tab has nothing to do with stays quiet$`,
			"src/ui/App.notifications.test.tsx",
			"notifications from the app a turn of a chat this tab has nothing to do with stays quiet"},
		{`^nothing is said while the person looks at the page$`,
			"src/ui/App.notifications.test.tsx",
			"notifications from the app nothing is said while the person looks at the page"},
		{`^a permission request on the turn the tab shows is announced$`,
			"src/ui/App.notifications.test.tsx",
			"notifications from the app a permission request on the turn the tab shows is announced"},
		{`^the service worker shows the same tag once when several tabs heard the event$`,
			"src/ui/pwa/serviceWorkerScript.test.ts",
			"the service worker shows the same tag once when several tabs heard the event"},
		{`^it shows nothing while another window of the app has the focus$`,
			"src/ui/pwa/serviceWorkerScript.test.ts",
			"the service worker shows nothing while another window of the app has the focus"},
	}

	suite := godog.TestSuite{
		Name: "web_ui_installable_app",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^the embedded web UI build$`, func() error {
				srv = httptest.NewServer(Handler())
				t.Cleanup(srv.Close)
				return nil
			})
			sc.Step(`^the page links the manifest$`, func() error {
				_, body, err := get("/")
				if err != nil {
					return err
				}
				if !strings.Contains(string(body), `<link rel="manifest" href="/manifest.webmanifest"`) {
					return fmt.Errorf("index.html does not link /manifest.webmanifest")
				}
				return nil
			})
			sc.Step(`^the manifest names the app, its window and its icons$`, func() error {
				m, err := readManifest()
				if err != nil {
					return err
				}
				if m.Name != "Coddy Agent" || m.ShortName != "Coddy" {
					return fmt.Errorf("manifest names %q / %q", m.Name, m.ShortName)
				}
				if m.ID != "/" || m.StartURL != "/" || m.Scope != "/" || m.Display != "standalone" {
					return fmt.Errorf("manifest id %q start_url %q scope %q display %q", m.ID, m.StartURL, m.Scope, m.Display)
				}
				var any192, any512, maskable bool
				for _, icon := range m.Icons {
					switch {
					case icon.Purpose == "any" && icon.Sizes == "192x192":
						any192 = true
					case icon.Purpose == "any" && icon.Sizes == "512x512":
						any512 = true
					case icon.Purpose == "maskable":
						maskable = true
					}
				}
				if !any192 || !any512 || !maskable {
					return fmt.Errorf("manifest icons lack a 192 or 512 px icon or a maskable one: %+v", m.Icons)
				}
				return nil
			})
			sc.Step(`^every icon the manifest names is served as a PNG of its size$`, func() error {
				m, err := readManifest()
				if err != nil {
					return err
				}
				for _, icon := range m.Icons {
					res, body, err := get(icon.Src)
					if err != nil {
						return err
					}
					if ct := res.Header.Get("Content-Type"); ct != "image/png" || icon.Type != "image/png" {
						return fmt.Errorf("%s: Content-Type %q, declared %q", icon.Src, ct, icon.Type)
					}
					cfg, format, err := image.DecodeConfig(strings.NewReader(string(body)))
					if err != nil || format != "png" {
						return fmt.Errorf("%s is not a PNG (%v)", icon.Src, err)
					}
					if got := fmt.Sprintf("%dx%d", cfg.Width, cfg.Height); got != icon.Sizes {
						return fmt.Errorf("%s is %s, the manifest says %s", icon.Src, got, icon.Sizes)
					}
				}
				return nil
			})
			sc.Step(`^the service worker is served from the root and revalidated on every load$`, func() error {
				res, body, err := get("/sw.js")
				if err != nil {
					return err
				}
				if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
					return fmt.Errorf("sw.js Content-Type %q", ct)
				}
				if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
					return fmt.Errorf("sw.js Cache-Control %q, want no-cache", cc)
				}
				// It caches nothing: no fetch handler stands between the page
				// and the server.
				if strings.Contains(string(body), `addEventListener("fetch"`) {
					return fmt.Errorf("sw.js handles fetch")
				}
				return nil
			})
			for _, s := range vitest {
				file, name := s.file, s.name
				sc.Step(s.step, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_installable_app.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI installable app feature failed")
	}
}
