//go:build scheduler

package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseCronUTC_MatchesStandardFiveField(t *testing.T) {
	_, err := ParseCronUTC("0 9 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}
}

func TestNextScheduledUTCHourly(t *testing.T) {
	s, err := ParseCronUTC("0 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	last, err := time.Parse(time.RFC3339, "2020-01-15T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	next := NextScheduledUTC(s, last)
	if want := "2020-01-15T11:00:00Z"; next.Format(time.RFC3339) != want {
		t.Fatalf("got %s want %s", next.Format(time.RFC3339), want)
	}
}

func TestStatePathLockPath(t *testing.T) {
	p := filepath.FromSlash("/x/y/job.md")
	if g := StatePath(p); g != filepath.FromSlash("/x/y/job.state") {
		t.Fatalf("StatePath %q", g)
	}
	if g := LockPath(p); g != filepath.FromSlash("/x/y/job.lock") {
		t.Fatalf("LockPath %q", g)
	}
}

func TestParseJobFromBytes(t *testing.T) {
	raw := `---
description: "Test"
schedule: "0 0 * * *"
---
Do something
`
	fm, err := ParseJobFromBytes([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Description != "Test" {
		t.Fatalf("description %q", fm.Description)
	}
}

func TestReadWriteJobStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "job.state")
	slot, err := time.Parse(time.RFC3339, "2024-06-01T12:30:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteJobState(p, slot); err != nil {
		t.Fatal(err)
	}
	got, err := ReadJobState(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(slot.UTC()) {
		t.Fatalf("ReadJobState got %v want %v", got, slot.UTC())
	}
	empty, err := ReadJobState(filepath.Join(dir, "missing.state"))
	if err != nil {
		t.Fatal(err)
	}
	if !empty.IsZero() {
		t.Fatalf("missing file should yield zero time, got %v", empty)
	}
}

func TestListFlatJobMarkdownFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "b.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := ListFlatJobMarkdownFiles([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("want 1 flat md, got %d: %v", len(paths), paths)
	}
	if filepath.Base(paths[0]) != "a.md" {
		t.Fatalf("unexpected path %q", paths[0])
	}
}
