//go:build swarm

package swarm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

func TestBindingsDirectorySyncFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names")
	r, _ := newTestRegistry(t, 0)
	if err := r.SetBindingsFile(path); err != nil {
		t.Fatal(err)
	}
	restore := platform.InjectDirSyncFault()
	_, err := r.Register(tunnelRequest("node"))
	if !errors.Is(err, ErrBindingsPersistence) || !errors.Is(err, platform.ErrWriteNotDurable) {
		restore()
		t.Fatalf("register error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		restore()
		t.Fatal(err)
	}
	if _, err := r.Register(tunnelRequest("other")); err == nil {
		restore()
		t.Fatal("pending durability accepted registration")
	}
	if _, err := r.Delete("node"); err == nil {
		restore()
		t.Fatal("pending durability accepted deletion")
	}
	restore()
	if _, err := r.Register(tunnelRequest("other")); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if _, err := r.Register(tunnelRequest("node")); !errors.Is(err, ErrNameBound) {
		t.Fatalf("lost committed binding: %v", err)
	}
	if deleted, err := r.Delete("node"); err != nil || !deleted {
		t.Fatalf("delete %v %v", deleted, err)
	}
}

func TestBindingsDeleteDirectorySyncFailure(t *testing.T) {
	r, _ := newTestRegistry(t, 0)
	if err := r.SetBindingsFile(filepath.Join(t.TempDir(), "names")); err != nil {
		t.Fatal(err)
	}
	resp, err := r.Register(tunnelRequest("node"))
	if err != nil {
		t.Fatal(err)
	}
	restore := platform.InjectDirSyncFault()
	deleted, err := r.Delete("node")
	if deleted || !errors.Is(err, platform.ErrWriteNotDurable) {
		restore()
		t.Fatalf("delete=%v err=%v", deleted, err)
	}
	req := tunnelRequest("node")
	req.LeaseSecret = resp.LeaseSecret
	if _, err := r.Register(req); err == nil {
		restore()
		t.Fatal("pending deletion allowed reconnect")
	}
	restore()
	if _, err := r.Register(req); err != nil {
		t.Fatalf("registration after recovery: %v", err)
	}
}
