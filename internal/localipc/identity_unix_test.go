//go:build !windows

package localipc

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestEndpointAddressDoesNotDependOnTemporaryDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	_, first, err := endpointAddress(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir())
	_, second, err := endpointAddress(home)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("the same home has different endpoints when TMPDIR changes: %q and %q", first, second)
	}
}

func TestConnectResolvesHomeSymlinks(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "home-link")
	if err := os.Symlink(home, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	owner, err := Claim(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = owner.Serve(ctx, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})) }()
	if _, err := Connect(ctx, alias); err != nil {
		t.Fatalf("the same home reached through a symlink must find its owner: %v", err)
	}
}
