//go:build scheduler

package storage

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestTrustStoreBindsWorkspaceJobAndDigest(t *testing.T) {
	store := NewTrustStore(t.TempDir())
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := Digest([]byte("job body"))
	if store.Approved(workspace, "nightly", d) {
		t.Fatal("an empty store approved a job")
	}
	if err := store.Approve(workspace, "nightly", d); err != nil {
		t.Fatal(err)
	}
	if !store.Approved(workspace, "nightly", d) {
		t.Fatal("the receipt does not approve the digest it was issued for")
	}
	if store.Approved(workspace, "nightly", Digest([]byte("edited"))) {
		t.Fatal("a receipt approved other content")
	}
	if store.Approved("/other", "nightly", d) || store.Approved(workspace, "weekly", d) {
		t.Fatal("a receipt leaked to another workspace or job")
	}
	if got := store.Workspaces(); len(got) != 1 || got[0] != workspace {
		t.Fatalf("workspaces = %v", got)
	}
	if removed, err := store.Revoke(workspace, "nightly"); err != nil || !removed {
		t.Fatalf("revoke = %v, %v", removed, err)
	}
	if store.Approved(workspace, "nightly", d) || len(store.Workspaces()) != 0 {
		t.Fatal("a revoked receipt still approves")
	}
}

func TestTrustStoreRenameMovesTheReceipt(t *testing.T) {
	store := NewTrustStore(t.TempDir())
	d := Digest([]byte("x"))
	if err := store.Approve("/ws", "old", d); err != nil {
		t.Fatal(err)
	}
	if err := store.Rename("/ws", "old", "new"); err != nil {
		t.Fatal(err)
	}
	if store.Approved("/ws", "old", d) || !store.Approved("/ws", "new", d) {
		t.Fatal("rename did not move the receipt")
	}
}

func TestTrustStoreConcurrentApprovalsAreAllKept(t *testing.T) {
	home := t.TempDir()
	var wg sync.WaitGroup
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := NewTrustStore(home).Approve("/ws", id, Digest([]byte(id))); err != nil {
				t.Error(err)
			}
		}(id)
	}
	wg.Wait()
	if got := NewTrustStore(home).Records("/ws"); len(got) != len(ids) {
		t.Fatalf("records = %d, want %d", len(got), len(ids))
	}
}
