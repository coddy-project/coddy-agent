//go:build desktop && http && ui

package main

import (
	"testing"

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
