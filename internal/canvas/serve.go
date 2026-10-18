package canvas

import (
	"bytes"
	_ "embed"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/webfs"
)

// BridgeProtocol names the messages the bridge and the web UI exchange.
const BridgeProtocol = "coddy-canvas/1"

// BridgePath is where a frame finds the bridge, relative to its root. A
// dot-named path never comes from disk, so it cannot collide with a file of
// the page.
const BridgePath = ".coddy/bridge.js"

// MaxInjectBytes is the largest page the bridge is put into; a larger one is
// served as it is, without picking or reports.
const MaxInjectBytes = 10 << 20

//go:embed bridge.js
var bridgeJS []byte

var bridgeModTime = time.Now()

// sandboxTokens are what a frame may do: run its scripts, handle its forms
// (form-action keeps them from going anywhere) and show alert / confirm. Never
// allow-same-origin, which would give the page the web UI's origin, nor
// top navigation or popups.
const sandboxTokens = "sandbox allow-scripts allow-forms allow-modals"

// ServeOptions are the operator's settings for serving frames.
type ServeOptions struct {
	// AllowedSources are the CSP sources a frame may load from and connect to
	// besides its own files (tools.canvas.allowed_sources).
	AllowedSources []string
}

// ValidateSource checks one entry of the sources a frame may load from (see
// webfs.ValidateCSPSource).
func ValidateSource(s string) error { return webfs.ValidateCSPSource(s) }

// ContentSecurityPolicy is the policy every response of a frame carries: the
// sandbox, which holds even when the frame's address is opened in a tab of its
// own, and the places the page may load from.
func ContentSecurityPolicy(sources []string) string {
	var extra strings.Builder
	for _, s := range sources {
		s = strings.TrimSpace(s)
		if ValidateSource(s) == nil {
			extra.WriteString(" ")
			extra.WriteString(s)
		}
	}
	srcs := extra.String()
	return sandboxTokens +
		"; default-src 'self' data: blob: 'unsafe-inline' 'unsafe-eval'" + srcs +
		"; connect-src 'self'" + srcs +
		"; form-action 'none'; base-uri 'self'; frame-ancestors *"
}

func setFrameHeaders(h http.Header, opts ServeOptions) {
	h.Set("Content-Security-Policy", ContentSecurityPolicy(opts.AllowedSources))
	h.Set("X-Content-Type-Options", "nosniff")
	// The frame's capability is in its address, and a page loading a CDN
	// script must not hand it on in a Referer.
	h.Set("Referrer-Policy", "no-referrer")
	// The page runs in an opaque origin: its own module scripts and fetches
	// are cross-origin requests. No credentials ride on them.
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Cache-Control", "no-store")
}

// ServeFrame answers one request for a file of a frame. rel is the part of the
// URL path after the capability, unescaped and without a leading slash. The
// caller has checked the capability and that the frame is on the board.
func ServeFrame(w http.ResponseWriter, r *http.Request, f Frame, rel string, opts ServeOptions) {
	setFrameHeaders(w.Header(), opts)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "the canvas is read-only", http.StatusMethodNotAllowed)
		return
	}
	rel = strings.TrimPrefix(rel, "/")
	if rel == BridgePath {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		http.ServeContent(w, r, "bridge.js", bridgeModTime, bytes.NewReader(bridgeJS))
		return
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			http.NotFound(w, r)
			return
		}
	}
	clean := path.Clean("/" + rel)
	if webfs.HasDotSegment(clean) {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(f.RootAbs())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = root.Close() }()
	name := strings.TrimPrefix(clean, "/")
	if name == "" {
		name = "."
	}
	file, info, err := openNoLinks(root, name)
	if errors.Is(err, errIsDir) {
		if rel != "" && !strings.HasSuffix(rel, "/") {
			// A folder's index opens with the folder's own address, so the
			// page's relative addresses resolve inside it.
			http.Redirect(w, r, path.Base(clean)+"/", http.StatusMovedPermanently)
			return
		}
		name = path.Join(name, "index.html")
		file, info, err = openNoLinks(root, name)
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = file.Close() }()
	if ct, ok := webfs.ContentType(name); ok {
		w.Header().Set("Content-Type", ct)
	} else if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if webfs.IsHTML(name) && info.Size() <= MaxInjectBytes {
		raw, err := io.ReadAll(io.LimitReader(file, MaxInjectBytes+1))
		if err != nil {
			http.Error(w, "read failed", http.StatusInternalServerError)
			return
		}
		page := InjectBridge(raw, BridgeSrc(rel))
		http.ServeContent(w, r, path.Base(name), info.ModTime(), bytes.NewReader(page))
		return
	}
	http.ServeContent(w, r, path.Base(name), info.ModTime(), file)
}

var errIsDir = errors.New("is a directory")

// openNoLinks opens a regular file of the root whose every path element is a
// real folder or, last, a regular file: a frame serves no symbolic links at
// all, so a link inside the root cannot lead to a dot-named file, and nothing
// is decided on a path that is resolved again later. The file opened is
// checked to be the one inspected, so a link swapped in between the check and
// the open is refused too. A folder is errIsDir.
func openNoLinks(root *os.Root, name string) (*os.File, fs.FileInfo, error) {
	var last fs.FileInfo
	if name == "." {
		info, err := root.Lstat(".")
		if err != nil {
			return nil, nil, err
		}
		last = info
	} else {
		parts := strings.Split(name, "/")
		for i := range parts {
			info, err := root.Lstat(path.Join(parts[:i+1]...))
			if err != nil {
				return nil, nil, err
			}
			if info.Mode()&fs.ModeSymlink != 0 {
				return nil, nil, fs.ErrNotExist
			}
			if i < len(parts)-1 && !info.IsDir() {
				return nil, nil, fs.ErrNotExist
			}
			last = info
		}
	}
	if last.IsDir() {
		return nil, nil, errIsDir
	}
	if !last.Mode().IsRegular() {
		return nil, nil, fs.ErrNotExist
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(last, info) {
		_ = file.Close()
		return nil, nil, fs.ErrNotExist
	}
	return file, info, nil
}

// BridgeSrc is the bridge's address relative to the page at rel, so it
// resolves inside the frame's address wherever the browser reached it from:
// a node directly or a relay's mount.
func BridgeSrc(rel string) string {
	dir := rel
	if strings.HasSuffix(dir, "/") {
		dir = strings.TrimSuffix(dir, "/")
	} else {
		dir = path.Dir(dir)
	}
	depth := 0
	if dir != "." && dir != "" {
		depth = strings.Count(dir, "/") + 1
	}
	return strings.Repeat("../", depth) + BridgePath
}

var (
	headTagRe = regexp.MustCompile(`(?i)<head(?:\s[^>]*)?>`)
	htmlTagRe = regexp.MustCompile(`(?i)<html(?:\s[^>]*)?>`)
	doctypeRe = regexp.MustCompile(`(?i)^\s*<!doctype[^>]*>`)
)

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

// InjectBridge puts the referrer meta and the bridge script at the start of
// the page's head: after the head tag, else after the html tag, else after the
// doctype, else first - never before the doctype, which would switch the page
// to quirks mode.
func InjectBridge(page []byte, src string) []byte {
	tag := []byte(`<meta name="referrer" content="no-referrer"><script src="` + src + `"></script>`)
	start := 0
	if bytes.HasPrefix(page, utf8BOM) {
		start = len(utf8BOM)
	}
	at := start
	if m := headTagRe.FindIndex(page[start:]); m != nil {
		at = start + m[1]
	} else if m := htmlTagRe.FindIndex(page[start:]); m != nil {
		at = start + m[1]
	} else if m := doctypeRe.FindIndex(page[start:]); m != nil {
		at = start + m[1]
	}
	out := make([]byte, 0, len(page)+len(tag))
	out = append(out, page[:at]...)
	out = append(out, tag...)
	return append(out, page[at:]...)
}
