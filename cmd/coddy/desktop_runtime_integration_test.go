//go:build desktop && http && ui

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/desktop"
	"github.com/EvilFreelancer/coddy-agent/external/httpserver"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/localipc"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

type runtimeSubsystemProbe struct {
	mu     sync.Mutex
	starts int
	stops  int
}

func (p *runtimeSubsystemProbe) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.starts, p.stops
}

func TestDesktopUsesTheServiceRuntimeWithTokenAuthAndNoPublicHTTP(t *testing.T) {
	home := t.TempDir()
	off := false
	cfg := &config.Config{
		Paths:      config.Paths{Home: home, CWD: home},
		HTTPServer: config.HTTPServerConfig{Enabled: &off, AuthToken: "service-public-token"},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt := &serve.Runtime{}
	if err := rt.Init(serve.Options{CLI: config.CLIPaths{Home: home, CWD: home}, NeedsSessions: true, Log: log, Cfg: cfg}); err != nil {
		t.Fatal(err)
	}
	owner, err := localipc.Claim(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	}()
	probe := &runtimeSubsystemProbe{}
	background := serve.Subsystem{
		Kind: serve.KindScheduler, Available: true,
		Enabled: func(*config.Config) bool { return true },
		Run: func(ctx context.Context, _ *config.Config) error {
			probe.mu.Lock()
			probe.starts++
			probe.mu.Unlock()
			<-ctx.Done()
			probe.mu.Lock()
			probe.stops++
			probe.mu.Unlock()
			return nil
		},
	}
	all := runtimeIPCSubsystems([]serve.Subsystem{background}, rt, subsystemDeps{
		home: home, httpAuthTokens: []string{"service-public-token"}, metrics: newMetricsWiring(rt),
	}, owner)
	serviceCtx, stopService := context.WithCancel(context.Background())
	serviceDone := make(chan error, 1)
	sup := serve.NewSupervisor(log, all)
	go func() { serviceDone <- sup.Run(serviceCtx, cfg, nil) }()

	client, err := connectDesktopRuntime(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	bridgeCtx, closeWindow := context.WithCancel(context.Background())
	bridgeAddr := freeDesktopTestAddress(t)
	bridgeDone := make(chan error, 1)
	go func() {
		bridgeDone <- serveAttachedDesktop(client, true, desktop.NewWindowHandle())(bridgeCtx, bridgeAddr)
	}()
	base := "http://" + bridgeAddr
	waitDesktopHTTP(t, base+"/v1/models")

	res, err := http.Get(base + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the password-free desktop got %s with a token configured on the service", res.Status)
	}
	res, err = http.Get(base + "/coddy/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	var authMe map[string]any
	if err := json.NewDecoder(res.Body).Decode(&authMe); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if authMe["auth_required"] != false || authMe["login_required"] != false {
		t.Fatalf("the ordinary desktop drew an auth form: %v", authMe)
	}
	req, err := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "null")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("the bridge weakened the opaque-origin boundary: %s", res.Status)
	}

	res, err = http.Get(base + "/desktop/")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Coddy-Desktop") == "" {
		t.Fatalf("the ordinary tab shell was not served: %s, marker %q", res.Status, res.Header.Get("X-Coddy-Desktop"))
	}

	// The same bridge with --auth forwards, rather than replaces, the public
	// bearer credential and refuses an unauthenticated API call.
	closeWindow()
	if err := <-bridgeDone; err != nil {
		t.Fatal(err)
	}
	starts, stops := probe.counts()
	if starts != 1 || stops != 0 {
		t.Fatalf("closing the window touched the service subsystem: starts=%d stops=%d", starts, stops)
	}

	authCtx, stopAuthBridge := context.WithCancel(context.Background())
	defer stopAuthBridge()
	authDone := make(chan error, 1)
	go func() {
		authDone <- serveAttachedDesktop(client, false, desktop.NewWindowHandle())(authCtx, bridgeAddr)
	}()
	defer func() {
		stopAuthBridge()
		if err := <-authDone; err != nil {
			t.Error(err)
		}
	}()
	waitDesktopHTTPStatus(t, base+"/v1/models", http.StatusUnauthorized)
	req, _ = http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer service-public-token")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("--auth did not forward the service token: %s", res.Status)
	}
	if starts, stops = probe.counts(); starts != 1 || stops != 0 {
		t.Fatalf("the attached desktop duplicated or stopped background work: starts=%d stops=%d", starts, stops)
	}

	stopService()
	select {
	case err := <-serviceDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the service did not stop cleanly")
	}
	if _, stops = probe.counts(); stops != 1 {
		t.Fatalf("the service subsystem did not stop with its owner: %d", stops)
	}
}

func TestDesktopAttachedAuthSupportsPasswordOnlyService(t *testing.T) {
	home := t.TempDir()
	off := false
	cfg := &config.Config{Paths: config.Paths{Home: home, CWD: home}, HTTPServer: config.HTTPServerConfig{Enabled: &off}}
	rt := &serve.Runtime{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := rt.Init(serve.Options{CLI: config.CLIPaths{Home: home, CWD: home}, NeedsSessions: true, Log: log, Cfg: cfg}); err != nil {
		t.Fatal(err)
	}
	owner, err := localipc.Claim(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	}()
	all := runtimeIPCSubsystems(nil, rt, subsystemDeps{
		home: home, httpLogin: httpserverLogin("operator", "correct horse"), metrics: newMetricsWiring(rt),
	}, owner)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve.NewSupervisor(log, all).Run(ctx, cfg, nil) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	client, err := connectDesktopRuntime(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	bridgeCtx, closeBridge := context.WithCancel(context.Background())
	defer closeBridge()
	addr := freeDesktopTestAddress(t)
	bridgeDone := make(chan error, 1)
	go func() { bridgeDone <- serveAttachedDesktop(client, false, desktop.NewWindowHandle())(bridgeCtx, addr) }()
	defer func() {
		closeBridge()
		if err := <-bridgeDone; err != nil {
			t.Error(err)
		}
	}()
	base := "http://" + addr
	waitDesktopHTTPStatus(t, base+"/v1/models", http.StatusUnauthorized)

	jar, err := netCookieJar()
	if err != nil {
		t.Fatal(err)
	}
	browser := &http.Client{Jar: jar}
	req, _ := http.NewRequest(http.MethodPost, base+"/coddy/auth/login", strings.NewReader(`{"user":"operator","password":"correct horse"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := browser.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("password-only sign-in failed: %s", res.Status)
	}
	res, err = browser.Get(base + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the signed-in desktop could not use the service: %s", res.Status)
	}
}

func TestDesktopRefusesAnOlderRunningDaemonWithoutIPC(t *testing.T) {
	home := t.TempDir()
	rec := serve.Record{PID: os.Getpid(), WorkerPID: os.Getpid(), StartedAt: time.Now()}
	if err := serve.WriteRecord(home, rec); err != nil {
		t.Fatal(err)
	}
	_, err := connectDesktopRuntime(context.Background(), home)
	if err == nil || !strings.Contains(err.Error(), "restart") || !strings.Contains(err.Error(), "second runtime") {
		t.Fatalf("the old daemon did not produce a safe refusal: %v", err)
	}
}

func freeDesktopTestAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func waitDesktopHTTP(t *testing.T, url string) {
	t.Helper()
	waitDesktopHTTPStatus(t, url, http.StatusOK)
}

func waitDesktopHTTPStatus(t *testing.T, url string, status int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, err := http.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
			if res.StatusCode == status {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not answer %d (last response %v, error %v)", url, status, res, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// These small adapters keep the test's imports focused on the runtime rather
// than on browser implementation details.
func httpserverLogin(user, password string) httpserver.LoginCredentials {
	return httpserver.LoginCredentials{User: user, Password: password}
}

func netCookieJar() (http.CookieJar, error) { return cookiejar.New(nil) }
