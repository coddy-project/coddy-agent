//go:build ui

// Package ui holds static assets for the bundled SPA (embedded into the binary).
package ui

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

//go:embed index.html styles.css app.js events-worker.js coddy-favicon.svg favicon-32.png favicon.ico apple-touch-icon.png manifest.webmanifest sw.js icon-192.png icon-512.png icon-maskable-512.png all:chunks
var Assets embed.FS

// asset is what the handler keeps per embedded file: a strong validator of
// its bytes and, for a text file, a gzip copy made the first time a browser
// asks for one.
type asset struct {
	data   []byte
	etag   string
	gzOnce sync.Once
	gz     []byte
}

var (
	assetsOnce sync.Once
	assetIndex map[string]*asset
)

// indexAssets reads every embedded file once. An embedded file has no
// modification time, so without a content validator a revalidation of a
// fixed-name file (no-cache) could only ever answer with the whole file.
func indexAssets() map[string]*asset {
	assetsOnce.Do(func() {
		assetIndex = map[string]*asset{}
		_ = fs.WalkDir(Assets, ".", func(name string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			b, rerr := fs.ReadFile(Assets, name)
			if rerr != nil {
				return nil
			}
			sum := sha256.Sum256(b)
			assetIndex[name] = &asset{data: b, etag: hex.EncodeToString(sum[:12])}
			return nil
		})
	})
	return assetIndex
}

func (a *asset) gzipped() []byte {
	a.gzOnce.Do(func() {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		_, _ = zw.Write(a.data)
		_ = zw.Close()
		a.gz = buf.Bytes()
	})
	return a.gz
}

// compressible names the text files worth sending with gzip; pictures and
// fonts are compressed already.
func compressible(name string) bool {
	switch path.Ext(name) {
	case ".js", ".css", ".html", ".json", ".svg", ".webmanifest", ".map", ".txt":
		return true
	}
	return false
}

// acceptsGzip reports whether the request takes a gzip body (and does not
// refuse it with q=0).
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		token, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(token), "gzip") {
			continue
		}
		q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
		return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
	}
	return false
}

// Handler serves the bundled SPA and sets Cache-Control on the fixed asset paths
// so browsers revalidate after a rebuild (the URLs carry no content hash), while
// the content-hashed files under /chunks/ are cached for good. Every file
// carries a content ETag, so a revalidation answers 304, and a text file goes
// out gzip-compressed to a browser that accepts it.
// Both surfaces that can host the SPA - an agent's HTTP server and a relay -
// mount this same handler.
func Handler() http.Handler {
	next := http.FileServer(http.FS(Assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/index.html", "/app.js", "/events-worker.js", "/styles.css",
			"/coddy-favicon.svg", "/favicon-32.png", "/favicon.ico", "/apple-touch-icon.png",
			"/sw.js", "/icon-192.png", "/icon-512.png", "/icon-maskable-512.png":
			w.Header().Set("Cache-Control", "no-cache")
		case "/manifest.webmanifest":
			// The standard library knows no type for the extension, and a
			// sniffed text/plain is not what the browser asks for.
			w.Header().Set("Content-Type", "application/manifest+json")
			w.Header().Set("Cache-Control", "no-cache")
		default:
			// The renderers app.js loads on demand: a new build gives a changed
			// file a new name, so a browser never has to ask again.
			if strings.HasPrefix(r.URL.Path, "/chunks/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
		}
		serveAsset(w, r, next)
	})
}

// serveAsset answers with the embedded file the path names, its headers set
// by Handler: the gzip copy when the request takes one, else the file through
// the file server, both under a validator of their own. A range request, a
// path that is not a file and /index.html (which the file server redirects to
// /) are left to the file server as they were.
func serveAsset(w http.ResponseWriter, r *http.Request, next http.Handler) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	a := indexAssets()[name]
	if a == nil || r.URL.Path == "/index.html" {
		next.ServeHTTP(w, r)
		return
	}
	text := compressible(name)
	if text {
		w.Header().Add("Vary", "Accept-Encoding")
	}
	if text && acceptsGzip(r) && r.Header.Get("Range") == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		if w.Header().Get("Content-Type") == "" {
			if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
				w.Header().Set("Content-Type", ct)
			}
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("ETag", `"`+a.etag+`-gz"`)
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(a.gzipped()))
		return
	}
	w.Header().Set("ETag", `"`+a.etag+`"`)
	next.ServeHTTP(w, r)
}
