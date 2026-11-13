//go:build http

package httpserver

import (
	"compress/gzip"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// gzipJSON compresses the JSON answers of the API for a client that accepts
// gzip. The lists a client polls (sessions, a page of messages, the changes of
// a folder with their patches) are repetitive JSON that shrinks several times
// over, which a relay, a remote server or the Telegram Mini App feels on every
// poll. Only a body the handler labels application/json is compressed, decided
// when the handler writes its header: an event stream, a file, an error page
// and the web UI's assets (compressed ahead by external/ui) go out as written,
// and Flush reaches the client in both cases.
func gzipJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An upgrade request (the browser extension's WebSocket) hijacks the writer
		// through a plain `w.(http.Hijacker)` assertion the wrapper would hide, and
		// every browser sends Accept-Encoding: gzip on it.
		if r.Method == http.MethodHead || r.Header.Get("Upgrade") != "" || !acceptsGzipEncoding(r.Header.Get("Accept-Encoding")) {
			// The identity variant varies on Accept-Encoding too, so Vary belongs on it.
			if r.Header.Get("Upgrade") == "" {
				w.Header().Add("Vary", "Accept-Encoding")
			}
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipJSONWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

// acceptsGzipEncoding reports whether an Accept-Encoding header admits gzip:
// named with a non-zero weight. A malformed q weight counts as a refusal for
// that token, and the best weight across the gzip tokens wins.
func acceptsGzipEncoding(header string) bool {
	best := -1.0
	for _, part := range strings.Split(header, ",") {
		params := strings.Split(part, ";")
		if !strings.EqualFold(strings.TrimSpace(params[0]), "gzip") {
			continue
		}
		weight := 1.0
		for _, p := range params[1:] {
			k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(k), "q") {
				continue
			}
			if w, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				weight = w
			} else {
				weight = 0
			}
		}
		if weight > best {
			best = weight
		}
	}
	return best > 0
}

type gzipJSONWriter struct {
	http.ResponseWriter
	decided bool
	gz      *gzip.Writer
}

func (g *gzipJSONWriter) WriteHeader(code int) {
	if !g.decided {
		g.decided = true
		if g.compressible(code) {
			h := g.Header()
			h.Del("Content-Length")
			h.Set("Content-Encoding", "gzip")
			h.Add("Vary", "Accept-Encoding")
			g.gz = gzip.NewWriter(g.ResponseWriter)
		}
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipJSONWriter) compressible(code int) bool {
	if code < http.StatusOK || code == http.StatusNoContent || code == http.StatusNotModified {
		return false
	}
	h := g.Header()
	if h.Get("Content-Encoding") != "" || h.Get("Content-Range") != "" {
		return false
	}
	media, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	return err == nil && media == "application/json"
}

func (g *gzipJSONWriter) Write(p []byte) (int, error) {
	if !g.decided {
		g.WriteHeader(http.StatusOK)
	}
	if g.gz != nil {
		return g.gz.Write(p)
	}
	return g.ResponseWriter.Write(p)
}

// Flush pushes what is compressed so far, or the plain bytes, to the client.
// Flushing before anything was written decides the encoding first: flushing the
// raw writer would commit plain headers and a gzipped body written after them
// would reach the client with no Content-Encoding.
func (g *gzipJSONWriter) Flush() {
	if !g.decided {
		g.WriteHeader(http.StatusOK)
	}
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the connection's writer.
func (g *gzipJSONWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *gzipJSONWriter) close() {
	if g.gz != nil {
		_ = g.gz.Close()
	}
}
