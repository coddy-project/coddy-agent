// Package preview implements the preview_server tool: a static file server over
// a project directory, started on a free port so the operator can open HTML, JS
// and CSS work in a browser. The server runs on goroutines inside coddy, as a
// background task of the session that asked for it.
package preview

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/webfs"
)

// handlerOptions is what newHandler needs besides the files.
type handlerOptions struct {
	// AllowedHosts are the Host header values (without a port) the server
	// answers to. Empty means any: the operator bound a non-loopback address
	// and clients arrive under whatever name reaches it.
	AllowedHosts []string
	// Log receives one line per request. Nil discards them.
	Log io.Writer
}

// newHandler serves files read-only with the rules a preview needs: no dot
// files, no caching, correct script types, and no answer to a foreign Host.
func newHandler(root *os.Root, opts handlerOptions) http.Handler {
	static := http.FileServerFS(webfs.DotHiddenFS{Files: root.FS()})
	rootDir := root.Name()
	// hiddenTarget resolves the request path against this directory: if the
	// root itself was reached through a symlink (a t.TempDir under a linked
	// /tmp, for instance) the resolved target would land outside it and the
	// check would quietly skip, so the root is resolved once up front.
	if resolved, err := filepath.EvalSymlinks(rootDir); err == nil {
		rootDir = resolved
	}
	allowed := map[string]bool{}
	for _, h := range opts.AllowedHosts {
		if h = normalizeHost(h); h != "" {
			allowed[h] = true
		}
	}
	log := &requestLog{w: opts.Log}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() { log.line(r, rec.status) }()

		// A page on another origin cannot read this server's answers, unless
		// it re-points its own name at 127.0.0.1 (DNS rebinding). Such a
		// request still carries the foreign name in Host.
		if len(allowed) > 0 && !allowed[normalizeHost(hostOnly(r.Host))] {
			http.Error(rec, "preview server: unexpected Host header", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			rec.Header().Set("Allow", "GET, HEAD")
			http.Error(rec, "preview server is read-only", http.StatusMethodNotAllowed)
			return
		}
		if webfs.HasDotSegment(r.URL.Path) {
			http.NotFound(rec, r)
			return
		}
		// A symlink inside the directory can point at a dot-file the request
		// path does not reveal; the resolved target answers under the same
		// rule.
		if webfs.HiddenTarget(rootDir, r.URL.Path) {
			http.NotFound(rec, r)
			return
		}

		// The page is being edited while it is open: every reload reads the disk.
		rec.Header().Set("Cache-Control", "no-store")
		if ct, ok := webfs.ContentType(r.URL.Path); ok {
			rec.Header().Set("Content-Type", ct)
		}
		static.ServeHTTP(rec, r)
	})
}

// hostOnly strips the port from a Host header, keeping an IPv6 literal whole.
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

func normalizeHost(h string) string {
	h = strings.TrimSpace(strings.ToLower(h))
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	return strings.TrimSuffix(h, ".")
}

// statusRecorder remembers the status for the request log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// requestLog writes the access log the agent reads through background_output:
// a 404 on a script is usually the whole answer to "the page is blank".
type requestLog struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *requestLog) line(r *http.Request, status int) {
	if l == nil || l.w == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.w, "%s %s %d\n", r.Method, r.URL.RequestURI(), status)
}
