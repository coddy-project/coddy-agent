//go:build metrics

package metrics

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
)

// What a scrape is held to: at most maxScrapesInFlight at once, each within
// scrapeTimeout, and shutdownGrace to finish once the exporter is asked to stop.
const (
	maxScrapesInFlight = 2
	scrapeTimeout      = 10 * time.Second
	shutdownGrace      = 5 * time.Second
	expositionType     = "text/plain; version=0.0.4; charset=utf-8"
)

// Options configure Serve.
type Options struct {
	// Addr is the address to listen on.
	Addr string
	// Cfg is the live configuration; the token is read from it on every request.
	Cfg func() *config.Config
	Log *slog.Logger
	// Collector holds the counters to serve. It outlives Serve, so a listener
	// rebuilt on another address continues them.
	Collector *Collector
}

// Serve runs the exporter until ctx is cancelled. A bind error returns at once.
func Serve(ctx context.Context, opts Options) error {
	if opts.Collector == nil || opts.Cfg == nil || opts.Log == nil {
		return errors.New("metrics: Collector, Cfg and Log are required")
	}
	log := logger.Component(opts.Log, "metrics")
	token := func() string {
		if cfg := opts.Cfg(); cfg != nil {
			return cfg.Metrics.Token
		}
		return ""
	}
	ln, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return err
	}
	if cfg := opts.Cfg(); cfg != nil && cfg.Metrics.ExposedWithoutToken() {
		log.Warn(exposedWarning, "addr", opts.Addr)
	}
	log.Info("metrics exporter listening", "addr", ln.Addr().String())

	server := httpx.NewServer(opts.Addr, opts.Collector.Handler(token))
	errs := make(chan error, 1)
	go func() { errs <- server.Serve(ln) }()

	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Warn("metrics shutdown timed out, closing anyway", "grace", shutdownGrace)
			_ = server.Close()
		}
		return nil
	}
}

// exposedWarning is what the log says of an exporter open beyond this machine
// with no token, at its start and when a reload leaves it so.
const exposedWarning = "the metrics exporter listens beyond this machine with no token: anybody who can reach the port reads its figures (set metrics.token, or bind metrics.host to 127.0.0.1)"

// Handler serves GET (and so HEAD) /metrics and answers 404 for every other
// path. token is read on every request: empty leaves the endpoint open,
// anything else must come as a bearer credential, checked before anything is
// read. Past that a scrape is held to maxScrapesInFlight at once across every
// listener of the collector, each within scrapeTimeout; a scrape that outlives
// its timeout keeps its place until it returns, so a stuck reader cannot pile
// up goroutines, not even across a rebuilt listener.
func (c *Collector) Handler(token func() string) http.Handler {
	scrape := http.TimeoutHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case c.inflight <- struct{}{}:
			defer func() { <-c.inflight }()
		default:
			http.Error(w, "too many scrapes in flight", http.StatusServiceUnavailable)
			return
		}
		body := c.exposition()
		w.Header().Set("Content-Type", expositionType)
		_, _ = w.Write(body)
	}), scrapeTimeout, "scrape timed out\n")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		if want := token(); want != "" && !bearerIs(r, want) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="coddy-metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		scrape.ServeHTTP(w, r)
	})
	return mux
}

// bearerIs compares the request's bearer credential with want in constant
// time. The scheme is case-insensitive and the credential trimmed, as for the
// HTTP API (external/httpserver is built under another tag, so the few lines
// are repeated here).
func bearerIs(r *http.Request, want string) bool {
	const scheme = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) < len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return false
	}
	got := strings.TrimSpace(h[len(scheme):])
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
