//go:build metrics

package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

func freeMetricsPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func scrapeMetrics(t *testing.T, addr, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func awaitMetricsListener(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("nothing listens on %s", addr)
}

// refuses reports whether nothing answers at addr any more.
func refuses(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return false
	}
	return true
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never happened: %s", what)
}

// The wiring runs the exporter over the runtime it was built for, under the
// supervisor like any other surface: the listener answers with the process's
// own figures, an address that moves is a new listener in front of the same
// counters, rebuilt in place, and switching the exporter off stops it.
func TestTheMetricsWiringServesTheRuntimeAndRebuildsOnANewAddress(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Paths: config.Paths{Home: home, CWD: home}}
	cfg.Metrics = config.MetricsConfig{Enable: true, Host: "127.0.0.1", Port: freeMetricsPort(t), Token: "scrape-me"}
	log := slog.New(slog.DiscardHandler)
	rt := &serve.Runtime{}
	if err := rt.Init(serve.Options{
		CLI: config.CLIPaths{Home: home, CWD: home}, SessionsRoot: filepath.Join(home, "sessions"),
		NeedsSessions: true, Log: log, Cfg: cfg,
	}); err != nil {
		t.Fatal(err)
	}
	wiring := newMetricsWiring(rt)
	stop := wiring.Start()
	t.Cleanup(stop)
	sub := wiring.Subsystem()
	if !sub.Available {
		t.Fatal("the exporter is not available in a build with the tag")
	}
	if got := sub.Fingerprint(cfg); got != cfg.Metrics.ListenAddr() {
		t.Fatalf("fingerprint %q, want the listen address %q", got, cfg.Metrics.ListenAddr())
	}
	tokenOnly := *cfg
	tokenOnly.Metrics.Token = "rotated"
	if sub.Fingerprint(&tokenOnly) != sub.Fingerprint(cfg) {
		t.Fatal("a rotated token moved the fingerprint: the token is read live and must not rebuild the listener")
	}

	keeper := serve.Subsystem{
		Kind: serve.KindScheduler, Available: true,
		Enabled: func(*config.Config) bool { return true },
		Run: func(ctx context.Context, _ *config.Config) error {
			<-ctx.Done()
			return nil
		},
	}
	sup := serve.NewSupervisor(log, []serve.Subsystem{keeper, sub})
	rt.SetSupervisor(sup)
	ctx, cancel := context.WithCancel(context.Background())
	reloads := make(chan *config.Config, 1)
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, cfg, reloads) }()
	t.Cleanup(func() { cancel(); <-done })

	first := cfg.Metrics.ListenAddr()
	awaitMetricsListener(t, first)
	if code, _ := scrapeMetrics(t, first, ""); code != http.StatusUnauthorized {
		t.Fatalf("scrape without the token: %d, want 401", code)
	}
	code, body := scrapeMetrics(t, first, "scrape-me")
	if code != http.StatusOK || !strings.Contains(body, "coddy_build_info") || !strings.Contains(body, "coddy_sessions_disk_size_bytes") {
		t.Fatalf("scrape with the token: %d\n%s", code, body)
	}
	if !strings.Contains(body, `coddy_subsystem_up{subsystem="scheduler"} 1`) || strings.Contains(body, `subsystem="metrics"`) {
		t.Fatalf("the subsystem table of the scrape:\n%s", body)
	}
	if !strings.Contains(body, "coddy_config_reloads_total 0") {
		t.Fatalf("no reload yet:\n%s", body)
	}

	// A new address: the supervisor rebuilds the exporter in place, and the
	// reload it was told of is already in the counter the new listener serves.
	moved := *cfg
	moved.Metrics.Port = freeMetricsPort(t)
	rt.ReplaceConfig(&moved)
	reloads <- &moved
	second := moved.Metrics.ListenAddr()
	awaitMetricsListener(t, second)
	eventually(t, "the old address stops answering", func() bool { return refuses(first) })
	if _, body = scrapeMetrics(t, second, "scrape-me"); !strings.Contains(body, "coddy_config_reloads_total 1") {
		t.Fatalf("the counter did not continue across the rebuild:\n%s", body)
	}

	// A rotated token needs no rebuild.
	rotated := moved
	rotated.Metrics.Token = "rotated"
	rt.ReplaceConfig(&rotated)
	reloads <- &rotated
	if code, _ := scrapeMetrics(t, second, "scrape-me"); code != http.StatusUnauthorized {
		t.Fatalf("the old token after a rotation: %d, want 401", code)
	}
	if code, _ := scrapeMetrics(t, second, "rotated"); code != http.StatusOK {
		t.Fatalf("the new token after a rotation: %d, want 200", code)
	}

	// Switched off while the keeper still serves, the exporter stops.
	off := rotated
	off.Metrics.Enable = false
	rt.ReplaceConfig(&off)
	reloads <- &off
	eventually(t, "the disabled exporter stops answering", func() bool { return refuses(second) })
}
