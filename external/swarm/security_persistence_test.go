//go:build swarm

package swarm

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/netx"
)

func TestServeRejectsUndeliverableGeneratedToken(t *testing.T) {
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	oldStderr := os.Stderr
	os.Stderr = stderr
	t.Cleanup(func() { os.Stderr = oldStderr; _ = stderr.Close() })
	home := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(home, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Serve(ctx, Options{Cfg: cfg, Log: slog.New(slog.DiscardHandler), Home: home, ListenAddr: "127.0.0.1:0"}); err == nil {
		t.Fatal("Serve accepted an undeliverable generated token")
	}
}

func TestBindingsFailClosedAndRecover(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "swarm-names.json")
	if err := os.WriteFile(file, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: dir}}
	if _, err := New(cfg, slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("malformed bindings accepted at startup")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	r, _ := newTestRegistry(t, 0)
	if err := r.SetBindingsFile(file); err != nil {
		t.Fatal(err)
	}
	owner, err := r.Register(tunnelRequest("node"))
	if err != nil {
		t.Fatal(err)
	}
	// Replacing the destination with a directory makes rename fail on all platforms.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Register(tunnelRequest("new")); err == nil {
		t.Fatal("registration succeeded without persistence")
	}
	if _, ok := r.Node("new"); ok {
		t.Fatal("failed registration published a lease")
	}
	if deleted, err := r.Delete("node"); err == nil || deleted {
		t.Fatal("failed delete succeeded")
	}
	if err := r.Pin("node"); err == nil {
		t.Fatal("failed pin succeeded")
	}
	if _, ok := r.Node("node"); !ok {
		t.Fatal("failed mutation removed lease")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	// A successful mutation restores the original binding on disk.
	if _, err := r.Register(tunnelRequest("new")); err != nil {
		t.Fatal(err)
	}
	restarted, _ := newTestRegistry(t, 0)
	if err := restarted.SetBindingsFile(file); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Register(tunnelRequest("node")); !errors.Is(err, ErrNameBound) {
		t.Fatalf("takeover after restart: %v", err)
	}
	req := tunnelRequest("node")
	req.LeaseSecret = owner.LeaseSecret
	if _, err := restarted.Register(req); err != nil {
		t.Fatalf("owner reconnect: %v", err)
	}
}

func TestGeneratedTokenDeliveryAlternatives(t *testing.T) {
	home := filepath.Join(t.TempDir(), "missing")
	log := slog.New(slog.DiscardHandler)
	var terminal bytes.Buffer
	if written, err := announceGeneratedToken(home, "secret", log, &terminal, true); err != nil || written || !strings.Contains(terminal.String(), "secret") {
		t.Fatalf("terminal delivery: %v %v", written, err)
	}
	if written, err := announceGeneratedToken(home, "secret", log, failingWriter{}, true); err == nil || written {
		t.Fatal("failed terminal and file accepted")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestDeleteBoundNameWithoutLeaseRollsBackOnFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "names.json")
	r, _ := newTestRegistry(t, 0)
	if err := r.SetBindingsFile(file); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Register(tunnelRequest("node")); err != nil {
		t.Fatal(err)
	}
	restarted, _ := newTestRegistry(t, 0)
	if err := restarted.SetBindingsFile(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	if deleted, err := restarted.Delete("node"); err == nil || deleted {
		t.Fatal("delete succeeded without saving")
	}
	if _, err := restarted.Register(tunnelRequest("node")); !errors.Is(err, ErrNameBound) {
		t.Fatalf("binding lost: %v", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if deleted, err := restarted.Delete("node"); err != nil || !deleted {
		t.Fatalf("delete after recovery: %v", err)
	}
}

func TestConfiguredOverridePersistsBeforePublishing(t *testing.T) {
	file := filepath.Join(t.TempDir(), "names.json")
	r, _ := newTestRegistry(t, 0)
	if err := r.SetBindingsFile(file); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Register(tunnelRequest("node")); err != nil {
		t.Fatal(err)
	}
	restarted, _ := newTestRegistry(t, 0)
	if err := restarted.SetBindingsFile(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.registerWithDial(tunnelRequest("node"), netx.Options{}, true); err == nil {
		t.Fatal("override succeeded without save")
	}
	if _, err := restarted.Register(tunnelRequest("node")); !errors.Is(err, ErrNameBound) {
		t.Fatalf("failed override freed name: %v", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.registerWithDial(tunnelRequest("node"), netx.Options{}, true); err != nil {
		t.Fatal(err)
	}
	if !restarted.nodes["node"].pinned {
		t.Fatal("configured lease not pinned")
	}
}
