//go:build http

package httpserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func wallpaperTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = home
	s := New(cfg, nil, slog.Default(), home)
	s.SetExtraAuthTokens([]string{"wallpaper-test-token"})
	t.Cleanup(s.Drain)
	return s, home
}

func wallpaperTestPicture(t *testing.T, home, name string) []byte {
	t.Helper()
	return wallpaperTestPictureDimensions(t, home, name, 1280, 720)
}

func wallpaperTestPictureDimensions(t *testing.T, home, name string, width, height int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "wallpapers")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func wallpaperRequest(s *Server, method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestWallpapersHTTP(t *testing.T) {
	s, home := wallpaperTestServer(t)
	raw := wallpaperTestPicture(t, home, "night sky.png")
	wallpaperTestPicture(t, home, "A.png")
	w := wallpaperRequest(s, "GET", "/coddy/wallpapers", "wallpaper-test-token")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"wallpapers":[{"name":"A.png"},{"name":"night sky.png"}]}` {
		t.Fatalf("gallery: %d %s", w.Code, w.Body.String())
	}
	w = wallpaperRequest(s, "GET", "/coddy/wallpapers/"+url.PathEscape("night sky.png"), "wallpaper-test-token")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), raw) {
		t.Fatalf("picture: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "image/png" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: %v", w.Header())
	}
	w = wallpaperRequest(s, "HEAD", "/coddy/wallpapers/A.png", "wallpaper-test-token")
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != strconv.Itoa(len(raw)) {
		t.Fatalf("HEAD: %d, headers %v, bytes %d", w.Code, w.Header(), w.Body.Len())
	}
}

func TestWallpapersAuth(t *testing.T) {
	s, home := wallpaperTestServer(t)
	wallpaperTestPicture(t, home, "sky.png")
	for _, path := range []string{"/coddy/wallpapers", "/coddy/wallpapers/sky.png"} {
		for _, token := range []string{"", "wrong"} {
			if w := wallpaperRequest(s, "GET", path, token); w.Code != 401 {
				t.Errorf("%s token %q: %d", path, token, w.Code)
			}
		}
		if w := wallpaperRequest(s, "GET", path+"?access_token=wallpaper-test-token", ""); w.Code != 401 {
			t.Errorf("query token: %d", w.Code)
		}
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Origin", "null")
		r.Header.Set("Authorization", "Bearer wallpaper-test-token")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Errorf("opaque origin: %d", w.Code)
		}
	}
}

func TestWallpapersCookieAuth(t *testing.T) {
	s := newLoginServer(t, configuredLogin(t))
	wallpaperTestPicture(t, s.activeCfg().Paths.Home, "sky.png")
	cookie := sessionCookieOf(t, signIn(t, s, loginTestUser, loginTestPassword))
	for _, path := range []string{"/coddy/wallpapers", "/coddy/wallpapers/sky.png"} {
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Errorf("cookie read %s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestWallpapersHTTPErrors(t *testing.T) {
	s, home := wallpaperTestServer(t)
	if w := wallpaperRequest(s, "GET", "/coddy/wallpapers", "wallpaper-test-token"); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"wallpapers":[]}` {
		t.Fatalf("missing dir: %d %s", w.Code, w.Body.String())
	}
	wallpaperTestPicture(t, home, "ok.png")
	if err := os.WriteFile(filepath.Join(home, "wallpapers", "bad.png"), []byte("not a picture"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(home, "wallpapers", "oversize.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(20<<20 + 1); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"missing.png", 404}, {"bad.png", 415}, {"oversize.png", 415}, {".hidden", 400}, {"%2e%2e%2fsecret", 400}, {"a%5cb.png", 400}, {"C%3Asecret", 400},
	} {
		w := wallpaperRequest(s, "GET", "/coddy/wallpapers/"+tc.path, "wallpaper-test-token")
		if w.Code != tc.status {
			t.Errorf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), home) {
			t.Errorf("filesystem path leaked: %s", w.Body.String())
		}
	}
	if w := wallpaperRequest(s, "POST", "/coddy/wallpapers", "wallpaper-test-token"); w.Code != 404 {
		t.Errorf("upload: %d", w.Code)
	}
	if err := os.RemoveAll(filepath.Join(home, "wallpapers")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "wallpapers"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := wallpaperRequest(s, "GET", "/coddy/wallpapers", "wallpaper-test-token")
	if w.Code != 500 || strings.Contains(w.Body.String(), home) {
		t.Fatalf("directory error: %d %s", w.Code, w.Body.String())
	}
}

func TestWallpapersOpenAPI(t *testing.T) {
	doc := openAPISpec()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/coddy/wallpapers", "/coddy/wallpapers/{name}"} {
		entry, ok := doc["paths"].(map[string]interface{})[path].(map[string]interface{})
		if !ok {
			t.Errorf("missing path %s", path)
			continue
		}
		get := entry["get"].(map[string]interface{})
		for _, status := range []string{"200", "401", "500"} {
			if get["responses"].(map[string]interface{})[status] == nil {
				t.Errorf("%s lacks %s", path, status)
			}
		}
	}
	item := doc["paths"].(map[string]interface{})["/coddy/wallpapers/{name}"].(map[string]interface{})
	for _, method := range []string{"get", "head"} {
		operation, ok := item[method].(map[string]interface{})
		if !ok {
			t.Errorf("missing %s", method)
			continue
		}
		if operation["responses"].(map[string]interface{})["413"] != nil {
			t.Errorf("%s advertises a byte cap", method)
		}
	}
	if !bytes.Contains(raw, []byte("image/webp")) {
		t.Error("missing WebP response media type")
	}
}

func TestWallpapersStreamLargeOriginal(t *testing.T) {
	s, home := wallpaperTestServer(t)
	wallpaperTestPicture(t, home, "large.png")
	path := filepath.Join(home, "wallpapers", "large.png")
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	const size = 64 << 20
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := sha256.New()
	if _, err := io.Copy(expected, source); err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			r, err := http.NewRequest(method, server.URL+"/coddy/wallpapers/large.png", nil)
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("Authorization", "Bearer wallpaper-test-token")
			response, err := server.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != 200 || response.ContentLength != size || response.Header.Get("Content-Type") != "image/png" {
				t.Fatalf("status %d, headers %v", response.StatusCode, response.Header)
			}
			actual := sha256.New()
			count, err := io.Copy(actual, response.Body)
			if err != nil {
				t.Fatal(err)
			}
			runtime.ReadMemStats(&after)
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
				t.Fatalf("%s of 64 MiB original allocated %d bytes", method, allocated)
			}
			if method == http.MethodHead {
				if count != 0 {
					t.Fatalf("HEAD sent %d bytes", count)
				}
				return
			}
			if count != size || !bytes.Equal(actual.Sum(nil), expected.Sum(nil)) {
				t.Fatalf("original changed, length %d", count)
			}
		})
	}
}
