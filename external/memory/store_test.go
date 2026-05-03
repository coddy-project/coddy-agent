package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSearch(t *testing.T) {
	tmp := t.TempDir()
	g := filepath.Join(tmp, "g")
	p := filepath.Join(tmp, "p")
	if err := os.MkdirAll(g, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g, "prefs.md"), []byte("User prefers tabs and Go modules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "proj.md"), []byte("This repo uses Makefile for build"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &Store{globalRoot: g, projectRoot: p}
	hits, err := st.Search("tabs golang", "both", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 1 {
		t.Fatalf("expected hits, got %d", len(hits))
	}
}

func TestSlugify(t *testing.T) {
	if g := slugify("  Hello World!!  "); g != "hello-world" {
		t.Fatalf("got %q", g)
	}
	if g := slugify(""); g != "note" {
		t.Fatalf("got %q", g)
	}
}
