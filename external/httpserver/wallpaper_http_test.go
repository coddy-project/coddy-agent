//go:build http

package httpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
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
	"github.com/EvilFreelancer/coddy-agent/internal/wallpaper"
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

func wallpaperBodyRequest(s *Server, method, name string, body io.Reader, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/coddy/wallpapers/"+url.PathEscape(name), body)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestWallpapersUploadDeduplicateReadDelete(t *testing.T) {
	s, home := wallpaperTestServer(t)
	raw := wallpaperTestPictureDimensions(t, t.TempDir(), "source.png", 1, 1)
	for _, tc := range []struct {
		name   string
		status int
		json   string
	}{
		{"night sky.png", 201, `{"wallpaper":{"name":"night sky.png"},"created":true}`},
		{"different.jpg", 200, `{"wallpaper":{"name":"night sky.png"},"created":false}`},
	} {
		w := wallpaperBodyRequest(s, "POST", tc.name, bytes.NewReader(raw), "wallpaper-test-token")
		if w.Code != tc.status || strings.TrimSpace(w.Body.String()) != tc.json {
			t.Fatalf("upload %s: %d %s", tc.name, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("upload cache header %v", w.Header())
		}
	}
	if w := wallpaperRequest(s, "GET", "/coddy/wallpapers", "wallpaper-test-token"); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"wallpapers":[{"name":"night sky.png"}]}` {
		t.Fatalf("gallery: %d %s", w.Code, w.Body.String())
	}
	if w := wallpaperBodyRequest(s, "GET", "night sky.png", nil, "wallpaper-test-token"); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), raw) {
		t.Fatalf("original: %d", w.Code)
	}
	w := wallpaperBodyRequest(s, "DELETE", "night sky.png", nil, "wallpaper-test-token")
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if w := wallpaperBodyRequest(s, "GET", "night sky.png", nil, "wallpaper-test-token"); w.Code != 404 {
		t.Fatalf("deleted image: %d", w.Code)
	}
	files, err := os.ReadDir(filepath.Join(home, "wallpapers"))
	if err != nil || len(files) != 0 {
		t.Fatalf("left files after delete: %v, %v", files, err)
	}
}

func TestWallpapersMutationAuth(t *testing.T) {
	s, home := wallpaperTestServer(t)
	raw := wallpaperTestPictureDimensions(t, home, "keep.png", 1, 1)
	for _, method := range []string{"POST", "DELETE"} {
		for _, token := range []string{"", "wrong"} {
			if w := wallpaperBodyRequest(s, method, "keep.png", bytes.NewReader(raw), token); w.Code != 401 {
				t.Errorf("%s token %q: %d", method, token, w.Code)
			}
		}
		r := httptest.NewRequest(method, "/coddy/wallpapers/keep.png?access_token=wallpaper-test-token", bytes.NewReader(raw))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Errorf("%s query token: %d", method, w.Code)
		}
		r = httptest.NewRequest(method, "/coddy/wallpapers/keep.png", bytes.NewReader(raw))
		r.Header.Set("Origin", "null")
		r.Header.Set("Authorization", "Bearer wallpaper-test-token")
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Errorf("%s opaque origin: %d", method, w.Code)
		}
	}
	if w := wallpaperBodyRequest(s, "GET", "keep.png", nil, "wallpaper-test-token"); !bytes.Equal(w.Body.Bytes(), raw) {
		t.Fatal("unauthorized mutation changed original")
	}
}

func TestWallpapersMutationCookieAuth(t *testing.T) {
	s := newLoginServer(t, configuredLogin(t))
	raw := wallpaperTestPictureDimensions(t, t.TempDir(), "source.png", 1, 1)
	cookie := sessionCookieOf(t, signIn(t, s, loginTestUser, loginTestPassword))
	for _, tc := range []struct {
		method string
		status int
	}{{"POST", 201}, {"DELETE", 204}} {
		r := httptest.NewRequest(tc.method, "/coddy/wallpapers/cookie.png", bytes.NewReader(raw))
		r.AddCookie(cookie)
		forged := r.Clone(r.Context())
		forged.Header.Set("Origin", "https://other.example")
		denied := httptest.NewRecorder()
		s.Handler().ServeHTTP(denied, forged)
		if denied.Code != 403 {
			t.Errorf("%s cross-site cookie: %d", tc.method, denied.Code)
		}
		r.Header.Set("Origin", "http://example.com")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s cookie: %d %s", tc.method, w.Code, w.Body.String())
		}
	}
}

func TestWallpapersMutationErrors(t *testing.T) {
	s, home := wallpaperTestServer(t)
	raw := wallpaperTestPictureDimensions(t, home, "keep.png", 1, 1)
	if err := os.WriteFile(filepath.Join(home, "wallpapers", "notes.txt"), []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"POST", "DELETE"} {
		for _, name := range []string{".hidden", "../escape.png", `a\b.png`, "C:stream"} {
			w := wallpaperBodyRequest(s, method, name, bytes.NewReader(raw), "wallpaper-test-token")
			if w.Code != 400 {
				t.Errorf("%s %s: %d %s", method, name, w.Code, w.Body.String())
			}
		}
	}
	for _, tc := range []struct {
		method, name string
		body         []byte
		status       int
	}{
		{"POST", "bad.png", []byte("not an image"), 415}, {"POST", "empty.png", nil, 415},
		{"DELETE", "notes.txt", nil, 415}, {"DELETE", "missing.png", nil, 404},
	} {
		w := wallpaperBodyRequest(s, tc.method, tc.name, bytes.NewReader(tc.body), "wallpaper-test-token")
		if w.Code != tc.status || strings.Contains(w.Body.String(), home) {
			t.Errorf("%s %s: %d %s", tc.method, tc.name, w.Code, w.Body.String())
		}
	}
}

func TestWallpapersMutationOpenAPI(t *testing.T) {
	item := openAPISpec()["paths"].(map[string]interface{})["/coddy/wallpapers/{name}"].(map[string]interface{})
	for method, statuses := range map[string][]string{"post": {"200", "201", "400", "401", "403", "415", "500"}, "delete": {"204", "400", "401", "403", "404", "415", "500"}} {
		operation, ok := item[method].(map[string]interface{})
		if !ok {
			t.Errorf("missing %s", method)
			continue
		}
		responses := operation["responses"].(map[string]interface{})
		for _, status := range statuses {
			if responses[status] == nil {
				t.Errorf("%s lacks %s", method, status)
			}
		}
		if responses["413"] != nil {
			t.Errorf("%s imposes a byte cap", method)
		}
		if method == "post" {
			content := operation["requestBody"].(map[string]interface{})["content"].(map[string]interface{})
			if content["application/octet-stream"] == nil {
				t.Error("POST lacks raw binary upload")
			}
			for _, status := range []string{"200", "201"} {
				schema := responses[status].(map[string]interface{})["content"].(map[string]interface{})["application/json"].(map[string]interface{})["schema"].(map[string]interface{})
				fields := schema["properties"].(map[string]interface{})
				if len(fields) != 2 || fields["wallpaper"] == nil || fields["created"] == nil {
					t.Errorf("POST schema: %v", schema)
				}
			}
		}
	}
}

func TestWallpaperStorageErrorTakesPrecedence(t *testing.T) {
	w := httptest.NewRecorder()
	writeWallpaperError(w, errors.Join(wallpaper.ErrInvalidImage, wallpaper.ErrUnavailable))
	if w.Code != 500 {
		t.Fatalf("cleanup/storage failure hidden by validation: %d %s", w.Code, w.Body.String())
	}
}

type wallpaperCancellingBody struct {
	io.Reader
	cancel context.CancelFunc
}

func (b wallpaperCancellingBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		b.cancel()
	}
	return n, err
}

func TestWallpapersCancelledUploadCleansTemporaryFile(t *testing.T) {
	s, home := wallpaperTestServer(t)
	raw := wallpaperTestPictureDimensions(t, t.TempDir(), "source.png", 1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := wallpaperCancellingBody{bytes.NewReader(raw), cancel}
	r := httptest.NewRequest("POST", "/coddy/wallpapers/cancelled.png", body).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer wallpaper-test-token")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 500 || strings.Contains(w.Body.String(), home) {
		t.Fatalf("cancelled response: %d %s", w.Code, w.Body.String())
	}
	entries, err := os.ReadDir(filepath.Join(home, "wallpapers"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancelled request left files: %v %v", entries, err)
	}
}

type zeroWallpaperBody struct{}

func (zeroWallpaperBody) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestWallpapersLargeUploadStreamsAndDeduplicates(t *testing.T) {
	s, home := wallpaperTestServer(t)
	raw := wallpaperTestPictureDimensions(t, t.TempDir(), "source.png", 1, 1)
	const padding = 32 << 20
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	for _, name := range []string{"large.png", "duplicate.png"} {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		body := io.MultiReader(bytes.NewReader(raw), io.LimitReader(zeroWallpaperBody{}, padding))
		request, err := http.NewRequest("POST", server.URL+"/coddy/wallpapers/"+name, body)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer wallpaper-test-token")
		request.Header.Set("Content-Type", "application/octet-stream")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 1024))
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		status, want := 200, `{"wallpaper":{"name":"large.png"},"created":false}`
		if name == "large.png" {
			status, want = 201, `{"wallpaper":{"name":"large.png"},"created":true}`
		}
		if response.StatusCode != status || strings.TrimSpace(string(data)) != want {
			t.Fatalf("large upload: %d %s", response.StatusCode, data)
		}
		runtime.ReadMemStats(&after)
		if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
			t.Fatalf("32 MiB HTTP upload allocated %d bytes", allocated)
		}
	}
	file, err := os.Open(filepath.Join(home, "wallpapers", "large.png"))
	if err != nil {
		t.Fatal(err)
	}
	actual, expected := sha256.New(), sha256.New()
	size, readErr := io.Copy(actual, file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("stored read %v, close %v", readErr, closeErr)
	}
	if _, err := io.Copy(expected, io.MultiReader(bytes.NewReader(raw), io.LimitReader(zeroWallpaperBody{}, padding))); err != nil {
		t.Fatal(err)
	}
	if size != int64(len(raw))+padding || !bytes.Equal(actual.Sum(nil), expected.Sum(nil)) {
		t.Fatalf("uploaded original changed: %d bytes", size)
	}
	entries, err := os.ReadDir(filepath.Join(home, "wallpapers"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "large.png" {
		t.Fatalf("extra entries: %v %v", entries, err)
	}
}

func TestWallpapersMutationsReportStorageErrors(t *testing.T) {
	s, home := wallpaperTestServer(t)
	raw := wallpaperTestPictureDimensions(t, t.TempDir(), "source.png", 1, 1)
	if err := os.WriteFile(filepath.Join(home, "wallpapers"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"POST", "DELETE"} {
		w := wallpaperBodyRequest(s, method, "picture.png", bytes.NewReader(raw), "wallpaper-test-token")
		if w.Code != 500 || strings.Contains(w.Body.String(), home) {
			t.Errorf("%s: %d %s", method, w.Code, w.Body.String())
		}
	}
}
