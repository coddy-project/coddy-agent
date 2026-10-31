package wallpaper

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestEnsureDefaultsLifecycle(t *testing.T) {
	home := t.TempDir()
	if err := EnsureDefaults(context.Background(), home); err != nil {
		t.Fatal(err)
	}
	names := []string{"coddy-purple.png", "dunes.png", "hills.png", "night.png"}
	rows, err := List(home)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, row := range rows {
		got = append(got, row.Name)
	}
	if !reflect.DeepEqual(got, names) {
		t.Fatalf("names: %v", got)
	}
	for _, name := range names {
		actual, err := os.ReadFile(filepath.Join(home, "wallpapers", name))
		if err != nil {
			t.Fatal(err)
		}
		expected, err := os.ReadFile(filepath.Join("defaults", name))
		if err != nil {
			t.Fatal(err)
		}
		if sha256.Sum256(actual) != sha256.Sum256(expected) {
			t.Fatalf("bytes differ: %s", name)
		}
	}
	if err := Delete(context.Background(), home, names[0]); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDefaults(context.Background(), home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "wallpapers", names[0])); !os.IsNotExist(err) {
		t.Fatalf("deleted file restored: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(home, "wallpapers")); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDefaults(context.Background(), home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "wallpapers")); !os.IsNotExist(err) {
		t.Fatalf("gallery restored: %v", err)
	}
}

func TestEnsureDefaultsExistingGallery(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, "wallpapers")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if populated {
				if err := os.WriteFile(filepath.Join(dir, "mine"), []byte("mine"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := EnsureDefaults(context.Background(), home); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != map[bool]int{false: 0, true: 1}[populated] {
				t.Fatalf("entries: %v", entries)
			}
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			if err := EnsureDefaults(context.Background(), home); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("gallery recreated: %v", err)
			}
		})
	}
}

func TestEnsureDefaultsConcurrent(t *testing.T) {
	home := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- EnsureDefaults(context.Background(), home) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := List(home)
	if err != nil || len(rows) != 4 {
		t.Fatalf("rows %v: %v", rows, err)
	}
}

func TestEnsureDefaultsBadPaths(t *testing.T) {
	if EnsureDefaults(context.Background(), "") == nil {
		t.Fatal("empty home accepted")
	}
	home := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(home, "wallpapers")); err != nil {
		t.Skip(err)
	}
	if EnsureDefaults(context.Background(), home) == nil {
		t.Fatal("gallery symlink accepted")
	}
	home = t.TempDir()
	if err := os.Symlink(filepath.Join(target, "marker"), filepath.Join(home, ".wallpapers-initialized")); err != nil {
		t.Skip(err)
	}
	if EnsureDefaults(context.Background(), home) == nil {
		t.Fatal("marker symlink accepted")
	}
}
