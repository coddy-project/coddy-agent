package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReadOnlyReplacesAnExistingAsset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thumbnail.png")
	if err := writeReadOnly(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writeReadOnly(path, []byte("repaired")); err != nil {
		t.Fatalf("replace a read-only asset: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "repaired" {
		t.Fatalf("asset = %q, %v", data, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("the replacement is not read-only: %v, %v", info, err)
	}
}
