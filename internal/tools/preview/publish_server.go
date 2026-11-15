package preview

// The static origin of the published-preview channel: one listener on the
// address tools.preview_publish names, serving the artifact root read-only.
//
// It is deliberately the dullest server in this repository. It has no API, no
// session, no token and no way to reach anything the publishing tool did not
// copy below its root, because the deployment it is for already decided who
// may read a preview: a reverse proxy authenticates the visitor and then
// forwards to this origin, which serves bytes. Everything that would make it
// cleverer - listing the root, following a link out of an artifact, answering
// a method that writes - is the thing that would make it dangerous behind
// somebody else's front door.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
	"github.com/EvilFreelancer/coddy-agent/internal/webfs"
)

// sweepInterval is how often the origin removes the artifacts whose lifetime
// has run out. A request for an expired artifact is refused whatever the
// sweeper has got to (publishHandler reads the manifest), so this only decides
// how long the bytes stay on the disk.
const sweepInterval = time.Minute

// shutdownGrace is how long an in-flight download may finish in when the
// origin stops.
const shutdownGrace = 5 * time.Second

// PublishOptions is what ServePublished needs to run the origin.
type PublishOptions struct {
	// Root is the artifact root. It is created when it does not exist: the
	// origin usually starts before anything has been published.
	Root string
	// ListenAddr is the fixed address the origin binds, host:port.
	ListenAddr string
	// PublicURL is the address the edge serves the root under. Its host joins
	// the Host allowlist of a loopback bind, so a proxy that forwards the
	// visitor's own Host is answered.
	PublicURL string
	// Log receives the origin's own lines. Nil discards them.
	Log *slog.Logger
}

// ServePublished runs the published-preview origin until ctx ends.
func ServePublished(ctx context.Context, opts PublishOptions) error {
	srv, err := listenPublished(opts)
	if err != nil {
		return err
	}
	return srv.serve(ctx)
}

// publishedServer is one running origin.
type publishedServer struct {
	store store
	root  *os.Root
	ln    net.Listener
	srv   *http.Server
	log   *slog.Logger
}

// listenPublished opens the artifact root and binds the address. Nothing is
// served yet, so a caller - a test, a supervisor - learns the address, and the
// failure to take it, before any request can arrive.
func listenPublished(opts PublishOptions) (*publishedServer, error) {
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		return nil, fmt.Errorf("the published-preview origin has no artifact root (tools.preview_publish.root)")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("preview root %s: %w", root, err)
	}
	opened, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", root, err)
	}
	ln, err := net.Listen("tcp", opts.ListenAddr)
	if err != nil {
		_ = opened.Close()
		return nil, fmt.Errorf("listen on %s: %w", opts.ListenAddr, err)
	}
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &publishedServer{store: store{root: root}, root: opened, ln: ln, log: log}
	s.srv = httpx.NewServer("", newPublishHandler(s.store, opened, publishHandlerOptions{
		AllowedHosts: allowedPublishHosts(ln.Addr(), opts.ListenAddr, opts.PublicURL),
		Log:          log,
	}))
	return s, nil
}

// addr is the address the origin actually bound, which is the configured one
// unless it asked for a free port.
func (s *publishedServer) addr() string { return s.ln.Addr().String() }

// serve answers requests, and sweeps expired artifacts while it does, until
// ctx ends.
func (s *publishedServer) serve(ctx context.Context) error {
	defer func() { _ = s.root.Close() }()
	s.store.sweep(time.Now())
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(sweepInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-t.C:
				if n := s.store.sweep(now); n > 0 {
					s.log.Debug("preview publish: expired artifacts removed", "count", n)
				}
			}
		}
	}()

	errc := make(chan error, 1)
	go func() {
		err := s.srv.Serve(s.ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errc <- err
	}()
	s.log.Info("preview publish: serving published previews", "addr", s.addr(), "root", s.store.root)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := s.srv.Shutdown(shutdown); err != nil {
			_ = s.srv.Close()
		}
		if err := <-errc; err != nil {
			s.log.Debug("preview publish: the origin ended with an error", "err", err)
		}
		return nil
	}
}

// allowedPublishHosts is the Host allowlist for a loopback bind, and nil (any
// host) for an address the operator opened to the network on purpose. The
// public address joins it because that is the Host a reverse proxy which
// preserves the visitor's own forwards, and the configured bind host because
// that is what one which rewrites it to the upstream sends.
func allowedPublishHosts(bound net.Addr, listenAddr, publicURL string) []string {
	if ta, ok := bound.(*net.TCPAddr); !ok || !ta.IP.IsLoopback() {
		return nil
	}
	hosts := append([]string(nil), loopbackNames...)
	if h, _, err := net.SplitHostPort(listenAddr); err == nil && h != "" {
		hosts = append(hosts, h)
	}
	if u, err := url.Parse(strings.TrimSpace(publicURL)); err == nil && u.Hostname() != "" {
		hosts = append(hosts, u.Hostname())
	}
	return hosts
}

// publishHandlerOptions is what newPublishHandler needs besides the files.
type publishHandlerOptions struct {
	// AllowedHosts are the Host header values (without a port) the origin
	// answers to. Empty means any.
	AllowedHosts []string
	// Log receives one line per refused request. Nil discards them.
	Log *slog.Logger
}

// newPublishHandler serves the artifact root read-only.
func newPublishHandler(st store, root *os.Root, opts publishHandlerOptions) http.Handler {
	static := http.FileServerFS(webfs.DotHiddenFS{Files: root.FS()})
	rootDir := root.Name()
	if resolved, err := filepath.EvalSymlinks(rootDir); err == nil {
		rootDir = resolved
	}
	allowed := map[string]bool{}
	for _, h := range opts.AllowedHosts {
		if h = normalizeHost(h); h != "" {
			allowed[h] = true
		}
	}
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(allowed) > 0 && !allowed[normalizeHost(hostOnly(r.Host))] {
			// The operator debugging their proxy needs this one: a proxy that
			// rewrites Host to something neither the bind host nor the public
			// address is refused, and nothing else says why.
			log.Debug("preview publish: refused a request under an unexpected Host",
				"host", r.Host, "path", r.URL.Path)
			http.Error(w, "published previews: unexpected Host header", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "published previews are read-only", http.StatusMethodNotAllowed)
			return
		}
		if webfs.HasDotSegment(r.URL.Path) || webfs.HiddenTarget(rootDir, r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		// The root itself is never an answer: a listing of it would hand
		// every visitor the address of every other artifact, and the only
		// address anybody was given is one artifact's.
		id := firstPathElement(r.URL.Path)
		if id == "" {
			http.NotFound(w, r)
			return
		}
		a, err := st.readManifest(id)
		if err != nil || a.expired(time.Now()) {
			log.Debug("preview publish: no artifact for this request",
				"artifact", id, "path", r.URL.Path, "expired", err == nil)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if ct, ok := webfs.ContentType(r.URL.Path); ok {
			w.Header().Set("Content-Type", ct)
		}
		static.ServeHTTP(w, r)
	})
}

// firstPathElement is the artifact id a request names, or "" for the root.
func firstPathElement(urlPath string) string {
	clean := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if clean == "" || clean == "." {
		return ""
	}
	if i := strings.IndexByte(clean, '/'); i >= 0 {
		return clean[:i]
	}
	return clean
}
