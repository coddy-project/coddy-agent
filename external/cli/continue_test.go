//go:build cli

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func writeSnapshotFixture(t *testing.T, root, id, cwd string, updatedAt time.Time) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Marshal properly: Windows paths carry backslashes that would corrupt
	// hand-concatenated JSON.
	meta, err := json.Marshal(map[string]string{
		"id":        id,
		"cwd":       cwd,
		"updatedAt": updatedAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLatestSessionIDPicksTheNewestForThisFolder(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	other := filepath.Join(root, "elsewhere")
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	now := time.Now()
	writeSnapshotFixture(t, store.Root, "sess-old", work, now.Add(-2*time.Hour))
	writeSnapshotFixture(t, store.Root, "sess-new", work, now.Add(-1*time.Minute))
	writeSnapshotFixture(t, store.Root, "sess-other", other, now)

	id, err := latestSessionID(store, work)
	if err != nil {
		t.Fatalf("latestSessionID: %v", err)
	}
	if id != "sess-new" {
		t.Fatalf("picked %q, want sess-new", id)
	}
}

func TestLatestSessionIDFailsClearlyWhenTheFolderHasNoSessions(t *testing.T) {
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	if err := os.MkdirAll(store.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := latestSessionID(store, filepath.Join(root, "empty"))
	if err == nil {
		t.Fatal("expected an error for a folder without sessions")
	}
}
