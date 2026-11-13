//go:build desktop && http && ui

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/desktop"
	"github.com/EvilFreelancer/coddy-agent/external/httpserver"
	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
	"github.com/EvilFreelancer/coddy-agent/internal/localipc"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

// connectDesktopRuntime attaches only after the authenticated IPC hello. An
// older recorded daemon without that endpoint is refused explicitly: starting
// another manager over its session store would recreate the bug this channel
// is meant to remove.
func connectDesktopRuntime(ctx context.Context, home string) (*localipc.Client, error) {
	client, err := localipc.Connect(ctx, home)
	if err == nil {
		return client, nil
	}
	if !errors.Is(err, localipc.ErrUnavailable) {
		return nil, fmt.Errorf("coddy desktop: the running service's local IPC failed its safety checks: %w", err)
	}
	if rec, readErr := serve.ReadRecord(home); readErr == nil && rec.Serving() {
		return nil, fmt.Errorf("coddy desktop: coddy serve (pid %d, worker %d) predates local runtime IPC; restart it with `coddy serve restart` instead of starting a second runtime", rec.PID, rec.WorkerPID)
	}
	if err := rejectExistingServiceWithoutIPC(home, serve.ServiceAgentHome(), serviceHint); err != nil {
		return nil, err
	}
	return nil, err
}

func rejectExistingServiceWithoutIPC(home, serviceHome string, state func() (string, bool)) error {
	actual, err := os.Stat(home)
	if err != nil {
		return nil
	}
	configured, err := os.Stat(serviceHome)
	if err != nil || !os.SameFile(actual, configured) {
		return nil
	}
	if hint, active := state(); active {
		return fmt.Errorf("coddy desktop: %s; restart the service to enable local runtime IPC", hint)
	}
	return nil
}

// serveAttachedDesktop serves only the local browser bridge. It has no
// Runtime, Supervisor or config watcher, so closing the window cannot stop the
// service or any of its background subsystems.
func serveAttachedDesktop(client *localipc.Client, noAuth bool, window *desktop.WindowHandle) func(context.Context, string) error {
	return func(ctx context.Context, addr string) error {
		upstream := client.ProxyHandler()
		withMode := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// A browser cannot select the private mode itself, least of all when
			// the operator explicitly asked for --auth.
			r.Header.Del(httpserver.DesktopNoAuthHeader)
			if noAuth {
				r.Header.Set(httpserver.DesktopNoAuthHeader, "1")
			}
			upstream.ServeHTTP(w, r)
		})
		srv := httpx.NewServer(addr, httpserver.DesktopBridgeHandler(withMode, window))
		errs := make(chan error, 1)
		go func() { errs <- srv.ListenAndServe() }()
		select {
		case err := <-errs:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := srv.Shutdown(shutdownCtx)
			if errors.Is(err, context.DeadlineExceeded) {
				// A streaming response belongs to the window, not the service.
				// Once the window is gone the bridge closes it explicitly.
				_ = srv.Close()
				return nil
			}
			return err
		}
	}
}
