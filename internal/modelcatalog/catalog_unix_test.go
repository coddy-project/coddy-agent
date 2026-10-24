//go:build !windows

package modelcatalog

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func runtimeIsUnix() bool { return true }

// A pipe in place of the copy would block its read for ever: it is refused,
// and the catalog is downloaded instead.
func TestLookupDoesNotHangOnAPipeInPlaceOfTheCopy(t *testing.T) {
	srv := newCatalogServer(t)
	home := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(home, CacheFileName), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	c, _, _ := testCatalog(t, home)
	done := make(chan error, 1)
	go func() {
		_, err := c.Lookup(context.Background(), cfgFor(srv.URL))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the lookup hangs on a pipe in place of the copy")
	}
	if srv.requests.Load() != 1 {
		t.Errorf("the catalog was not downloaded in place of the pipe")
	}
}
