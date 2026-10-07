//go:build ui

package ui

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// appJSBudget is the most app.js may weigh. Mermaid and KaTeX live in lazy
// chunks; the entry only gains the Markdown math parser and the components that
// call the renderers. Raise it deliberately, never to make room for a renderer.
const appJSBudget = 1_850_000

// Strings only the renderers' own code carries: their error messages.
var rendererMarkers = map[string]string{
	"mermaid": "No diagram type detected",
	"katex":   "KaTeX parse error",
}

var hashedChunk = regexp.MustCompile(`^chunks/[A-Za-z0-9._-]+-[A-Za-z0-9_-]{8,}\.js$`)

func TestDiagramsFeature(t *testing.T) {
	var res *httptest.ResponseRecorder
	suite := godog.TestSuite{
		Name: "web-ui-diagrams",
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			ctx.Step(`^the embedded web UI build$`, func() error {
				_, err := fs.Stat(Assets, "app.js")
				return err
			})
			ctx.Step(`^the diagram and formula renderers are not part of app\.js$`, func() error {
				app, err := fs.ReadFile(Assets, "app.js")
				if err != nil {
					return err
				}
				for name, marker := range rendererMarkers {
					if bytes.Contains(app, []byte(marker)) {
						return fmt.Errorf("app.js carries %s (%q)", name, marker)
					}
				}
				return nil
			})
			ctx.Step(`^app\.js stays within its size budget$`, func() error {
				info, err := fs.Stat(Assets, "app.js")
				if err != nil {
					return err
				}
				if info.Size() > appJSBudget {
					return fmt.Errorf("app.js is %d bytes, budget %d", info.Size(), appJSBudget)
				}
				return nil
			})
			ctx.Step(`^the renderers are embedded as content-hashed chunks$`, func() error {
				_, err := rendererChunks()
				return err
			})
			ctx.Step(`^a browser fetches a renderer chunk$`, func() error {
				chunks, err := rendererChunks()
				if err != nil {
					return err
				}
				chunk := chunks["katex"]
				res = httptest.NewRecorder()
				Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/"+chunk, nil))
				if res.Code != http.StatusOK {
					return fmt.Errorf("GET /%s: %d", chunk, res.Code)
				}
				return nil
			})
			ctx.Step(`^the chunk is served as JavaScript$`, func() error {
				if ct := res.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
					return fmt.Errorf("Content-Type %q", ct)
				}
				return nil
			})
			ctx.Step(`^the response may be cached for a year without revalidation$`, func() error {
				cc := res.Header().Get("Cache-Control")
				if !strings.Contains(cc, "max-age=31536000") || !strings.Contains(cc, "immutable") {
					return fmt.Errorf("Cache-Control %q", cc)
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/web_ui_diagrams.feature"}, TestingT: t},
	}
	if suite.Run() != 0 {
		t.Fatal("diagram scenarios failed")
	}
}

// The fixed names keep revalidating; only the hashed chunks are immutable.
func TestChunkCacheDoesNotLeakToFixedNames(t *testing.T) {
	for _, path := range []string{"/app.js", "/styles.css", "/index.html"} {
		res := httptest.NewRecorder()
		Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if cc := res.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s: Cache-Control %q, want no-cache", path, cc)
		}
	}
}

// rendererChunks walks the embedded chunks, checks every name carries a content
// hash and returns, per renderer, a chunk holding its code.
func rendererChunks() (map[string]string, error) {
	found := map[string]string{}
	err := fs.WalkDir(Assets, "chunks", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".js") {
			return err
		}
		if !hashedChunk.MatchString(path) {
			return fmt.Errorf("chunk %s has no content hash in its name", path)
		}
		body, err := fs.ReadFile(Assets, path)
		if err != nil {
			return err
		}
		for name, marker := range rendererMarkers {
			if bytes.Contains(body, []byte(marker)) {
				found[name] = path
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for name := range rendererMarkers {
		if found[name] == "" {
			return nil, fmt.Errorf("no embedded chunk carries %s", name)
		}
	}
	return found, nil
}
