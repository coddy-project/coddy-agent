//go:build http

package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestWallpaperDefaultsRealServeRestart(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = home
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	run := func(check func(*Server)) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		ready := make(chan *Server, 1)
		go func() {
			done <- Serve(ctx, Options{Cfg: cfg, Mgr: mgr, Log: slog.Default(), DefaultCWD: home, ListenAddr: "127.0.0.1:0", OnServer: func(s *Server) {
				if s != nil {
					ready <- s
				}
			}})
		}()
		select {
		case s := <-ready:
			check(s)
		case err := <-done:
			cancel()
			t.Fatalf("Serve: %v", err)
		case <-time.After(10 * time.Second):
			cancel()
			t.Fatal("Serve did not start")
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	run(func(s *Server) {
		w := wallpaperRequest(s, http.MethodGet, "/coddy/wallpapers", "")
		if w.Code != 200 || !containsDefaultNames(w.Body.String()) {
			t.Fatalf("gallery: %d %s", w.Code, w.Body.String())
		}
		w = wallpaperBodyRequest(s, http.MethodDelete, "dunes.png", nil, "")
		if w.Code != 204 {
			t.Fatalf("delete: %d %s", w.Code, w.Body.String())
		}
	})
	run(func(s *Server) {
		w := wallpaperRequest(s, http.MethodGet, "/coddy/wallpapers/dunes.png", "")
		if w.Code != 404 {
			t.Fatalf("deleted default returned: %d", w.Code)
		}
		if _, err := os.Stat(filepath.Join(home, ".wallpapers-initialized")); err != nil {
			t.Fatal(err)
		}
	})
}

func containsDefaultNames(s string) bool {
	for _, name := range []string{"dunes.png", "hills.png", "night.png", "coddy-purple.png"} {
		if !strings.Contains(s, name) {
			return false
		}
	}
	return true
}
