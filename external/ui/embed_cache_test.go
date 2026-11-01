//go:build ui

package ui

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
)

func get(t *testing.T, h http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A fixed-name asset carries a validator, so a revalidation answers 304
// instead of sending the whole file again.
func TestAssetsRevalidateWithAnETag(t *testing.T) {
	h := Handler()
	for _, path := range []string{"/app.js", "/styles.css", "/"} {
		first := get(t, h, http.MethodGet, path, nil)
		tag := first.Header().Get("ETag")
		if first.Code != http.StatusOK || tag == "" {
			t.Fatalf("%s: status %d, ETag %q; want 200 with an ETag", path, first.Code, tag)
		}
		again := get(t, h, http.MethodGet, path, map[string]string{"If-None-Match": tag})
		if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
			t.Fatalf("%s: revalidation status %d with %d bytes, want 304 and no body", path, again.Code, again.Body.Len())
		}
		if again.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s: Cache-Control %q on the 304", path, again.Header().Get("Cache-Control"))
		}
	}
}

// A browser that accepts gzip gets a compressed copy of a text asset that
// decompresses to the embedded bytes, under a validator of its own.
func TestTextAssetsAreSentCompressed(t *testing.T) {
	h := Handler()
	want, err := fs.ReadFile(Assets, "app.js")
	if err != nil {
		t.Fatal(err)
	}
	plain := get(t, h, http.MethodGet, "/app.js", nil)
	gz := get(t, h, http.MethodGet, "/app.js", map[string]string{"Accept-Encoding": "gzip, deflate, br"})
	if gz.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding %q, want gzip", gz.Header().Get("Content-Encoding"))
	}
	if gz.Header().Get("Vary") != "Accept-Encoding" || plain.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("Vary %q / %q, want Accept-Encoding on both", gz.Header().Get("Vary"), plain.Header().Get("Vary"))
	}
	if gz.Header().Get("ETag") == plain.Header().Get("ETag") {
		t.Fatal("the compressed and the plain copy share an ETag")
	}
	if gz.Body.Len() >= len(want)/2 {
		t.Fatalf("compressed body %d bytes for %d", gz.Body.Len(), len(want))
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(zr)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("decompressed body differs from the embedded app.js (err %v)", err)
	}
	if !bytes.Equal(plain.Body.Bytes(), want) || plain.Header().Get("Content-Encoding") != "" {
		t.Fatal("a client without gzip did not get the plain file")
	}
	again := get(t, h, http.MethodGet, "/app.js", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": gz.Header().Get("ETag")})
	if again.Code != http.StatusNotModified {
		t.Fatalf("revalidating the compressed copy: status %d, want 304", again.Code)
	}
}

// A range request is answered over the encoding the client accepts; a
// picture and a HEAD keep their usual answers.
func TestRangesPicturesAndHead(t *testing.T) {
	h := Handler()
	ranged := get(t, h, http.MethodGet, "/app.js", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-9"})
	gzFirst := indexAssets()["app.js"].gzipped()[:10]
	if ranged.Code != http.StatusPartialContent || ranged.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(ranged.Body.Bytes(), gzFirst) {
		t.Fatalf("gzip range: status %d, encoding %q, %d bytes", ranged.Code, ranged.Header().Get("Content-Encoding"), ranged.Body.Len())
	}
	plain := get(t, h, http.MethodGet, "/app.js", map[string]string{"Range": "bytes=0-9"})
	if plain.Code != http.StatusPartialContent || plain.Header().Get("Content-Encoding") != "" || plain.Body.Len() != 10 {
		t.Fatalf("plain range: status %d, encoding %q, %d bytes", plain.Code, plain.Header().Get("Content-Encoding"), plain.Body.Len())
	}
	png := get(t, h, http.MethodGet, "/icon-192.png", map[string]string{"Accept-Encoding": "gzip"})
	if png.Header().Get("Content-Encoding") != "" || png.Header().Get("ETag") == "" {
		t.Fatalf("png: encoding %q, ETag %q", png.Header().Get("Content-Encoding"), png.Header().Get("ETag"))
	}
	head := get(t, h, http.MethodHead, "/styles.css", map[string]string{"Accept-Encoding": "gzip"})
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("head: status %d, %d bytes, encoding %q", head.Code, head.Body.Len(), head.Header().Get("Content-Encoding"))
	}
	if mf := get(t, h, http.MethodGet, "/manifest.webmanifest", map[string]string{"Accept-Encoding": "gzip"}); mf.Header().Get("Content-Type") != "application/manifest+json" {
		t.Fatalf("manifest Content-Type %q", mf.Header().Get("Content-Type"))
	}
}

// The Accept-Encoding parser honours q-values, duplicate tokens and the
// wildcard, so a client that refused gzip never gets a gzip body.
func TestAcceptsGzipParsesQValues(t *testing.T) {
	for _, c := range []struct {
		header string
		want   bool
	}{
		{"gzip", true},
		{"gzip, deflate, br", true},
		{"GZIP", true},
		{"gzip;q=1", true},
		{"gzip;q=0.5", true},
		{"gzip;q=0", false},
		{"gzip;q=0.0", false},
		{"gzip;q=0.000", false},
		{"gzip;q=0.0000", false},
		{"gzip;foo=1;q=0", false},
		{"gzip, gzip;q=0", false},
		{"gzip;q=0, gzip", true},
		{"*", true},
		{"*;q=0", false},
		{"br", false},
		{"br;q=1, *;q=0.5", true},
		{"identity", false},
		{"", false},
	} {
		req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
		req.Header.Set("Accept-Encoding", c.header)
		if got := acceptsGzip(req); got != c.want {
			t.Fatalf("Accept-Encoding %q: got %v, want %v", c.header, got, c.want)
		}
	}
}

// Warming makes the gzip copy of every text file ahead of its first request.
func TestWarmAssetsCompressesTextFilesAhead(t *testing.T) {
	warmAssets()
	for name, a := range indexAssets() {
		if compressible(name) && len(a.gz) == 0 {
			t.Fatalf("%s has no gzip copy after warming", name)
		}
	}
}
