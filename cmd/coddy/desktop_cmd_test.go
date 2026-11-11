//go:build desktop && http && ui

package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/desktop"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

// The desktop's surface list is the whole of `coddy serve` beside the window's
// own loopback surface: every configured surface is marked auxiliary so a
// bind conflict or a token a gateway refuses stops it alone instead of taking
// the window down, and the window surface is the one non-auxiliary entry, on
// by definition - `httpserver.enable` does not speak for it.
func TestDesktopSubsystemsRunsEverythingBesideTheWindow(t *testing.T) {
	rt := &serve.Runtime{}
	deps := subsystemDeps{
		httpListenAddr:  func(*config.Config) string { return "127.0.0.1:12345" },
		swarmListenAddr: func(*config.Config) string { return "0.0.0.0:9876" },
		metrics:         newMetricsWiring(rt),
	}
	all := desktopSubsystems(rt, deps, "127.0.0.1:9", true, desktop.NewWindowHandle())

	window := all[0]
	if window.Kind != serve.KindDesktop {
		t.Fatalf("the first descriptor is %v, not the window surface", window.Kind)
	}
	if window.Auxiliary {
		t.Fatal("the window surface is auxiliary - its failure must end the run")
	}
	if !window.Enabled(&config.Config{}) {
		t.Fatal("an empty config disabled the window surface")
	}
	off := false
	if !window.Enabled(&config.Config{HTTPServer: config.HTTPServerConfig{Enabled: &off}}) {
		t.Fatal("httpserver.enable: false disabled the window surface")
	}
	if got := window.Address(&config.Config{}); got != "127.0.0.1:9" {
		t.Fatalf("window address = %q", got)
	}
	for _, sub := range all[1:] {
		if !sub.Auxiliary {
			t.Fatalf("%v is not auxiliary: its failure would take the window down", sub.Kind)
		}
	}
	// The configured surface keeps its own enabled key: the window opening is
	// no reason for a node with httpserver.enable: false to bind a public port.
	for _, sub := range all[1:] {
		if sub.Kind == serve.KindHTTP && sub.Enabled(&config.Config{HTTPServer: config.HTTPServerConfig{Enabled: &off}}) {
			t.Fatal("httpserver.enable: false still enabled the public surface")
		}
	}
}

// The exporters' collectors run for the whole process, as coddy serve's do:
// the desktop must start the wiring it hands its subsystem list, or a scrape
// on a process with metrics.enable answers every Coddy family empty.
func TestDesktopStartsTheMetricsWiring(t *testing.T) {
	src, err := os.ReadFile("desktop_cmd.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(src)
	if !strings.Contains(source, "defer wiring.Start()()") {
		t.Fatal("the desktop never starts the metrics wiring")
	}
	if strings.Contains(source, "metrics:         newMetricsWiring(rt),") {
		t.Fatal("the subsystem list gets a fresh wiring that is never started")
	}
}

type desktopTestLogBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *desktopTestLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *desktopTestLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func TestDesktopWarnsAboutEnabledUnavailableSubsystems(t *testing.T) {
	var out desktopTestLogBuffer
	log := slog.New(slog.NewTextHandler(&out, nil))
	cfg := &config.Config{Metrics: config.MetricsConfig{Enable: true}}
	all := []serve.Subsystem{
		{Kind: serve.KindMetrics, ConfigKey: "metrics.enable", BuildTag: "metrics", Enabled: func(c *config.Config) bool { return c.Metrics.Enable }},
		{Kind: serve.KindHTTP, ConfigKey: "httpserver.enable", BuildTag: "http", Available: true, Enabled: func(*config.Config) bool { return true }},
	}
	warnDesktopSubsystems(log, cfg, all)
	got := out.String()
	for _, want := range []string{"metrics.enable", "metrics", "not built", "desktop window"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "httpserver.enable") {
		t.Fatalf("an available subsystem was warned about: %q", got)
	}
}

// A coddy serve process may already own the configured public address. Its
// bind conflict is auxiliary to the desktop: the loopback window must keep
// serving instead of the supervisor cancelling every surface.
func TestDesktopPublicPortConflictLeavesWindowRunning(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupied.Close() }()
	windowListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	windowAddr := windowListener.Addr().String()
	if err := windowListener.Close(); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	var out desktopTestLogBuffer
	log := slog.New(slog.NewTextHandler(&out, nil))
	host, portText, err := net.SplitHostPort(occupied.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths:      config.Paths{Home: home, CWD: home},
		HTTPServer: config.HTTPServerConfig{Host: host, Port: port},
		Metrics:    config.MetricsConfig{Enable: true},
	}
	rt := &serve.Runtime{}
	if err := rt.Init(serve.Options{
		CLI: config.CLIPaths{Home: home, CWD: home}, NeedsSessions: true,
		Log: log, Cfg: cfg,
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- serveHTTP(rt, home, true, desktop.NewWindowHandle(), newMetricsWiring(rt), "")(ctx, windowAddr)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		res, requestErr := http.Get("http://" + windowAddr + "/v1/models")
		failed := strings.Contains(out.String(), "subsystem=httpserver") && strings.Contains(out.String(), "level=ERROR")
		if requestErr == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK && failed {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the window did not survive the public bind conflict; log:\n%s", out.String())
		}
		select {
		case err := <-done:
			t.Fatalf("the desktop supervisor ended on the auxiliary bind conflict: %v", err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := out.String(); !strings.Contains(got, "metrics.enable") || !strings.Contains(got, "desktop window") {
		t.Fatalf("the unavailable enabled subsystem was not warned about: %q", got)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the desktop supervisor returned the auxiliary error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the desktop supervisor did not stop")
	}
}
