// Package preview implements the preview_server tool: a static file server over
// a project directory, started on a free port so the operator can open HTML, JS
// and CSS work in a browser. The server runs on goroutines inside coddy, as a
// background task of the session that asked for it.
package preview

import (
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// contentTypes pins the types a page cannot work without. Go asks the host for
// the rest, and on Windows the host is the registry, where an installed editor
// routinely maps .js to text/plain - a type browsers refuse for an ES module.
var contentTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".htm":   "text/html; charset=utf-8",
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".json":  "application/json",
	".map":   "application/json",
	".mjs":   "text/javascript; charset=utf-8",
	".svg":   "image/svg+xml",
	".wasm":  "application/wasm",
	".woff":  "font/woff",
	".woff2": "font/woff2",
}

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
	static := http.FileServerFS(dotHiddenFS{files: root.FS()})
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
		if hasDotSegment(r.URL.Path) {
			http.NotFound(rec, r)
			return
		}
		// A symlink inside the directory can point at a dot-file the request
		// path does not reveal; the resolved target answers under the same
		// rule.
		if hiddenTarget(rootDir, r.URL.Path) {
			http.NotFound(rec, r)
			return
		}

		// The page is being edited while it is open: every reload reads the disk.
		rec.Header().Set("Cache-Control", "no-store")
		if ct, ok := contentTypes[strings.ToLower(path.Ext(r.URL.Path))]; ok {
			rec.Header().Set("Content-Type", ct)
		}
		static.ServeHTTP(rec, r)
	})
}

// hasDotSegment reports whether any element of the URL path starts with a dot:
// .git, .env, the Coddy store. Project secrets live behind such names, and a
// page under preview has no business with them.
func hasDotSegment(urlPath string) bool {
	for _, seg := range strings.Split(urlPath, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
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

// dotHiddenFS wraps the served filesystem so a directory listing never shows a
// dot-named entry - the same names hasDotSegment refuses on a request path.
type dotHiddenFS struct{ files fs.FS }

// Open returns the file the wrapped filesystem has, with the listing of a
// directory filtered of dot names. Only directories are wrapped: a regular
// file must keep its Seek for Range requests (a video or a download the
// browser reads in parts), which an interface-typed wrapper would strip.
func (d dotHiddenFS) Open(name string) (fs.File, error) {
	f, err := d.files.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.IsDir() {
		return f, err
	}
	if dir, ok := f.(fs.ReadDirFile); ok {
		return dotHiddenDir{File: f, dir: dir}, nil
	}
	return f, nil
}

// dotHiddenDir filters dot-named entries out of a directory read.
type dotHiddenDir struct {
	fs.File
	dir fs.ReadDirFile
}

// ReadDir drops the entries whose name starts with a dot. A page that comes
// back all dots is not the end of the directory - the next page is read, so a
// positive count never gets an empty result without an error.
func (d dotHiddenDir) ReadDir(count int) ([]fs.DirEntry, error) {
	for {
		entries, err := d.dir.ReadDir(count)
		if err != nil {
			return entries, err
		}
		kept := entries[:0]
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ".") {
				kept = append(kept, e)
			}
		}
		if len(kept) > 0 || count <= 0 {
			return kept, nil
		}
	}
}

// hiddenTarget reports whether the request path resolves, inside the served
// directory, to a dot-named element - the names hasDotSegment refuses on the
// URL itself. A symlink inside the directory can point at one: the file open
// stays confined by os.Root, so an escape still cannot be served; this only
// applies the same dot policy to what the symlink points at.
func hiddenTarget(rootDir, urlPath string) bool {
	resolved, err := filepath.EvalSymlinks(filepath.Join(rootDir, strings.TrimPrefix(path.Clean("/"+urlPath), "/")))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootDir, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		// Outside the directory: os.Root refuses the open itself.
		return false
	}
	return hiddenPathElement(filepath.ToSlash(rel))
}

// hiddenPathElement reports whether a slash-separated path holds an element
// that starts with a dot, where "." alone is just the current directory.
func hiddenPathElement(rel string) bool {
	for _, e := range strings.Split(rel, "/") {
		if len(e) > 1 && strings.HasPrefix(e, ".") {
			return true
		}
	}
	return false
}
