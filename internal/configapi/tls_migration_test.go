package configapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestRemovedTLSSettings(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			live, err := config.LoadWithPaths(config.Paths{Home: dir, CWD: dir, ConfigPath: path})
			if err != nil {
				t.Fatal(err)
			}
			installed := false
			b := Backend{Live: func() *config.Config { return live }, Install: func(*config.Config) error { installed = true; return nil }}
			mux := http.NewServeMux()
			b.Register(mux)
			route := "/coddy/config"
			if method == http.MethodPost {
				route += "/validate"
			}
			for _, body := range []string{
				`{"tls":null}`,
				`{"httpserver":{"tls":{}}}`,
				`{"HTTPServer":{"tls":{}}}`,
				`{"providers":[{"tls_auto":false}]}`,
				`{"providers":[{"TLS_AUTO":false}]}`,
				`{"providers":[{"\u0074\u006c\u0073_\u0061\u0075\u0074\u006f":false}]}`,
				`{"swarm":{"node_tls":null}}`,
			} {
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest(method, route, strings.NewReader(body)))
				if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "reverse proxy") {
					t.Errorf("%s: status %d: %s", body, w.Code, w.Body.String())
				}
				if installed {
					t.Fatal("invalid settings installed")
				}
				raw, err := os.ReadFile(path)
				if err != nil || string(raw) != "{}\n" {
					t.Fatalf("invalid settings changed file: %q %v", raw, err)
				}
			}
		})
	}
}
