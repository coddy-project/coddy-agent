package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListSnapshotsWithDoesNotRereadUnchangedMetadata(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sess_cached")
	writeBundle(t, dir, map[string]any{
		"version":      1,
		"id":           "sess_cached",
		"title":        "cached title",
		"updatedAt":    "2026-10-20T10:00:00Z",
		"messageCount": 1,
	}, 1)
	fs := &FileStore{Root: root}
	opts := ListOptions{IncludeEmpty: true}
	rows, err := fs.ListSnapshotsWith(opts)
	if err != nil || len(rows) != 1 {
		t.Fatalf("first listing = %v, %v; want one row", rows, err)
	}

	path := filepath.Join(dir, sessionMetaFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Preserve the file identity used by the cache while making a fresh read
	// fail. A second listing can succeed only by using the validated metadata.
	if err := os.WriteFile(path, []byte(strings.Repeat("x", len(before))), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}

	rows, err = fs.ListSnapshotsWith(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Title != "cached title" {
		t.Fatalf("second listing = %+v, want the cached row", rows)
	}
}

func TestListSnapshotsWithCacheReflectsBundleChanges(t *testing.T) {
	root := t.TempDir()
	firstDir := filepath.Join(root, "sess_first")
	writeBundle(t, firstDir, map[string]any{
		"version":      1,
		"id":           "sess_first",
		"title":        "before",
		"updatedAt":    "2026-10-20T10:00:00Z",
		"messageCount": 1,
	}, 1)
	fs := &FileStore{Root: root}
	opts := ListOptions{IncludeEmpty: true}
	if rows, err := fs.ListSnapshotsWith(opts); err != nil || len(rows) != 1 {
		t.Fatalf("first listing = %v, %v; want one row", rows, err)
	}

	// Replace the metadata atomically with a same-sized edit and a new mtime.
	path := filepath.Join(firstDir, sessionMetaFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	meta["title"] = "after!"
	edited, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if len(edited) != len(raw) {
		t.Fatalf("edit changed size from %d to %d; the fixture must keep it fixed", len(raw), len(edited))
	}
	if err := writeBytesAtomic(path, edited); err != nil {
		t.Fatal(err)
	}
	changed := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}

	rows, err := fs.ListSnapshotsWith(opts)
	if err != nil || len(rows) != 1 || rows[0].Title != "after!" {
		t.Fatalf("edited listing = %+v, %v; want the new title", rows, err)
	}

	secondDir := filepath.Join(root, "sess_second")
	writeBundle(t, secondDir, map[string]any{
		"version":      1,
		"id":           "sess_second",
		"title":        "added",
		"updatedAt":    "2026-10-21T10:00:00Z",
		"messageCount": 1,
	}, 1)
	rows, err = fs.ListSnapshotsWith(opts)
	if err != nil || len(rows) != 2 {
		t.Fatalf("listing after add = %+v, %v; want two rows", rows, err)
	}

	if err := os.RemoveAll(firstDir); err != nil {
		t.Fatal(err)
	}
	rows, err = fs.ListSnapshotsWith(opts)
	if err != nil || len(rows) != 1 || rows[0].SessionID != "sess_second" {
		t.Fatalf("listing after delete = %+v, %v; want only sess_second", rows, err)
	}
}
