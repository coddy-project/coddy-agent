//go:build desktop && http && ui

package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/desktop"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/localipc"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

// The public API can intentionally admit cross-origin clients; a password-free
// desktop window must not inherit that wider trust or accept rebinding hosts.
func TestDesktopSurfacesRejectUntrustedBrowserOrigins(t *testing.T) {
	for _, attached := range []bool{false, true} {
		name := "standalone"
		if attached {
			name = "attached"
		}
		t.Run(name, func(t *testing.T) {
			base := startDesktopSecurityStand(t, attached, true)
			parsed, err := url.Parse(base)
			if err != nil {
				t.Fatal(err)
			}
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				req, err := http.NewRequest(method, base+"/v1/models", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Origin", base)
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				_ = res.Body.Close()
				if res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusMisdirectedRequest {
					t.Fatalf("the desktop rejected its own %s request: %s", method, res.Status)
				}
			}
			tests := []struct {
				name   string
				method string
				host   string
				origin string
			}{
				{"rebound host", http.MethodGet, "attacker.example:" + parsed.Port(), "http://attacker.example:" + parsed.Port()},
				{"other loopback page reads", http.MethodGet, parsed.Host, "http://127.0.0.1:49151"},
				{"other loopback page writes", http.MethodPost, parsed.Host, "http://127.0.0.1:49151"},
				{"public website", http.MethodGet, parsed.Host, "https://attacker.example"},
			}
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					req, err := http.NewRequest(tc.method, base+"/v1/models", nil)
					if err != nil {
						t.Fatal(err)
					}
					req.Host = tc.host
					req.Header.Set("Origin", tc.origin)
					res, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := res.Body.Close(); err != nil {
							t.Error(err)
						}
					}()
					if res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusMisdirectedRequest {
						t.Fatalf("untrusted browser request reached the password-free desktop: %s (Access-Control-Allow-Origin %q)", res.Status, res.Header.Get("Access-Control-Allow-Origin"))
					}
					if got := res.Header.Get("Access-Control-Allow-Origin"); got != "" {
						t.Fatalf("an untrusted origin received CORS access: %q", got)
					}
				})
			}
		})
	}
}

func startDesktopSecurityStand(t *testing.T, attached, noAuth bool) string {
	t.Helper()
	home := t.TempDir()
	off := false
	cfg := &config.Config{
		Paths: config.Paths{Home: home, CWD: home},
		HTTPServer: config.HTTPServerConfig{
			Enabled: &off, AuthToken: "service-public-token", AllowInsecure: true,
			CORS: config.HTTPCORSConfig{Enabled: true, AllowLoopback: true, AllowedOrigins: []string{"*"}},
		},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt := &serve.Runtime{}
	if err := rt.Init(serve.Options{CLI: config.CLIPaths{Home: home, CWD: home}, NeedsSessions: true, Log: log, Cfg: cfg}); err != nil {
		t.Fatal(err)
	}
	window := desktop.NewWindowHandle()
	address := freeDesktopTestAddress(t)
	ctx, stop := context.WithCancel(context.Background())
	var serveWindow func(context.Context, string) error
	if attached {
		owner, err := localipc.Claim(home)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = owner.Close() })
		all := runtimeIPCSubsystems(nil, rt, subsystemDeps{home: home, metrics: newMetricsWiring(rt)}, owner)
		serviceCtx, stopService := context.WithCancel(context.Background())
		serviceDone := make(chan error, 1)
		go func() { serviceDone <- serve.NewSupervisor(log, all).Run(serviceCtx, cfg, nil) }()
		t.Cleanup(func() {
			stopService()
			select {
			case err := <-serviceDone:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("service did not stop")
			}
		})
		client, err := connectDesktopRuntime(context.Background(), home)
		if err != nil {
			t.Fatal(err)
		}
		serveWindow = serveAttachedDesktop(client, noAuth, window)
	} else {
		serveWindow = serveHTTP(rt, home, noAuth, window, newMetricsWiring(rt), "")
	}
	done := make(chan error, 1)
	go func() { done <- serveWindow(ctx, address) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("window did not stop")
		}
	})
	base := "http://" + address
	waitDesktopHTTP(t, base+"/v1/models")
	return base
}
