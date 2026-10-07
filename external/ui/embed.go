//go:build ui

// Package ui holds static assets for the bundled SPA (embedded into the binary).
package ui

import (
	"embed"
	"net/http"
	"strings"
)

//go:embed index.html styles.css app.js events-worker.js coddy-favicon.svg favicon-32.png favicon.ico apple-touch-icon.png all:chunks
var Assets embed.FS

// Handler serves the bundled SPA and sets Cache-Control on the fixed asset paths
// so browsers revalidate after a rebuild (the URLs carry no content hash), while
// the content-hashed files under /chunks/ are cached for good.
// Both surfaces that can host the SPA - an agent's HTTP server and a relay -
// mount this same handler.
func Handler() http.Handler {
	next := http.FileServer(http.FS(Assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/index.html", "/app.js", "/events-worker.js", "/styles.css",
			"/coddy-favicon.svg", "/favicon-32.png", "/favicon.ico", "/apple-touch-icon.png":
			w.Header().Set("Cache-Control", "no-cache")
		default:
			// The renderers app.js loads on demand: a new build gives a changed
			// file a new name, so a browser never has to ask again.
			if strings.HasPrefix(r.URL.Path, "/chunks/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
		}
		next.ServeHTTP(w, r)
	})
}
