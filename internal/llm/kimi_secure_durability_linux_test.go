//go:build linux || android

package llm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

func TestKimiExistingStoreIntentSyncFailureStopsRefresh(t *testing.T) {
	parent := privateKimiTestDir(t)
	dir := filepath.Join(parent, "store")
	// Model a peer that created the directory before this process opened it.
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store := NewKimiCredentialStore(dir)
	data, err := json.Marshal(expiringKimiCredential())
	if err != nil {
		t.Fatal(err)
	}
	// A peer has not synced this creation or any directory entry yet.
	if err := os.WriteFile(store.CredentialPath("global"), data, 0600); err != nil {
		t.Fatal(err)
	}
	server, httpServer := newKimiRefreshServer(t, okReply)
	refresher := newTestRefresher(t, store, httpServer.URL, httpServer.Client())
	restore := platform.InjectDirSyncFault()
	_, err = refresher.AccessToken(context.Background())
	restore()
	if !errors.Is(err, platform.ErrWriteNotDurable) {
		t.Fatalf("expected sync failure, got %v", err)
	}
	if got := server.calls.Load(); got != 0 {
		t.Fatalf("refresh sent %d requests after sync failure", got)
	}
}
