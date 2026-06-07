package session

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func makeDataURL(mime, content string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(content))
	return "data:" + mime + ";base64," + enc
}

func TestSavePartsToAssets_NoOp(t *testing.T) {
	// No sessionDir — nothing written, no error.
	parts := []llm.ImagePart{{DataURL: makeDataURL("text/plain", "hello"), Name: "a.txt"}}
	if err := SavePartsToAssets(parts, ""); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if parts[0].FilePath != "" {
		t.Fatalf("expected empty FilePath, got %q", parts[0].FilePath)
	}
}

func TestSavePartsToAssets_SingleFile(t *testing.T) {
	dir := t.TempDir()
	content := "The secret number is 42."
	parts := []llm.ImagePart{
		{DataURL: makeDataURL("text/plain", content), Name: "note.txt"},
	}
	if err := SavePartsToAssets(parts, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := filepath.Join(AssetsPath(dir), "note.txt")
	if parts[0].FilePath != want {
		t.Fatalf("FilePath = %q, want %q", parts[0].FilePath, want)
	}
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != content {
		t.Fatalf("content = %q, want %q", string(got), content)
	}
	// Verify read-only permissions.
	fi, _ := os.Stat(want)
	if fi.Mode().Perm() != 0o444 {
		t.Fatalf("perm = %o, want 0444", fi.Mode().Perm())
	}
}

func TestSavePartsToAssets_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	parts := []llm.ImagePart{
		{DataURL: makeDataURL("text/plain", "file1"), Name: "doc.txt"},
		{DataURL: makeDataURL("text/plain", "file2"), Name: "doc.txt"}, // duplicate name
		{DataURL: makeDataURL("text/plain", "file3"), Name: "img.png"},
	}
	if err := SavePartsToAssets(parts, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assetsDir := AssetsPath(dir)

	// First file should keep its name.
	if parts[0].FilePath != filepath.Join(assetsDir, "doc.txt") {
		t.Errorf("parts[0].FilePath = %q, want doc.txt", parts[0].FilePath)
	}
	// Duplicate should get a disambiguated name.
	if parts[1].FilePath == parts[0].FilePath {
		t.Errorf("duplicate file should have different path")
	}
	if parts[1].FilePath == "" {
		t.Errorf("duplicate file path should not be empty")
	}
	// Third file: different name, no collision.
	if parts[2].FilePath != filepath.Join(assetsDir, "img.png") {
		t.Errorf("parts[2].FilePath = %q, want img.png", parts[2].FilePath)
	}

	// All three files must have distinct paths and readable content.
	paths := map[string]bool{}
	for i, p := range parts {
		if paths[p.FilePath] {
			t.Errorf("parts[%d]: duplicate FilePath %q", i, p.FilePath)
		}
		paths[p.FilePath] = true
		if _, err := os.ReadFile(p.FilePath); err != nil {
			t.Errorf("parts[%d]: read %q: %v", i, p.FilePath, err)
		}
	}
}

func TestSavePartsToAssets_EmptyDataURL(t *testing.T) {
	dir := t.TempDir()
	parts := []llm.ImagePart{
		{DataURL: "", Name: "empty.txt"},
	}
	if err := SavePartsToAssets(parts, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parts[0].FilePath != "" {
		t.Fatalf("empty DataURL should leave FilePath empty")
	}
}
