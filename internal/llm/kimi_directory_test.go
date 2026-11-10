package llm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestKimiDirectorySyncsEveryAncestor(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "providers", "kimi")
	var synced []string
	if err := ensureKimiDirectory(dir, func(path string) error {
		synced = append(synced, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{dir, filepath.Dir(dir), root} {
		found := false
		for _, path := range synced {
			if path == want {
				found = true
			}
		}
		if !found {
			t.Errorf("directory %q not synced: %v", want, synced)
		}
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("store directory not created: %v", err)
	}
}

func TestKimiDirectorySyncFailureIsReturned(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "providers", "kimi")
	fault := errors.New("sync failure")
	if err := ensureKimiDirectory(dir, func(string) error { return fault }); !errors.Is(err, fault) {
		t.Fatalf("sync failure = %v", err)
	}
}
