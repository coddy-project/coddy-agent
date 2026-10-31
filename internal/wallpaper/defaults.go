package wallpaper

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

//go:embed defaults/*.png
var bundledDefaults embed.FS

const defaultsMarker = ".wallpapers-initialized"

// EnsureDefaults makes a one-time decision for this home. Claiming the marker
// before copying means an interrupted copy is never retried over user deletions.
func EnsureDefaults(ctx context.Context, home string) error {
	if home == "" {
		return ErrUnavailable
	}
	if err := lockMutations(ctx); err != nil {
		return err
	}
	defer unlockMutations()
	root, err := os.OpenRoot(home)
	if err != nil {
		return fmt.Errorf("wallpaper defaults home: %w", err)
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(defaultsMarker)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("wallpaper defaults marker is not a regular file")
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("wallpaper defaults marker: %w", err)
	}
	info, err = root.Lstat("wallpapers")
	if err == nil && (!info.IsDir() || info.Mode()&fs.ModeSymlink != 0) {
		return fmt.Errorf("wallpaper gallery is not a directory")
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("wallpaper gallery: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	marker, err := root.OpenFile(defaultsMarker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, fs.ErrExist) {
		info, err = root.Lstat(defaultsMarker)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("wallpaper defaults marker is not a regular file")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("create wallpaper defaults marker: %w", err)
	}
	syncErr := marker.Sync()
	closeErr := marker.Close()
	if syncErr != nil {
		return fmt.Errorf("sync wallpaper defaults marker: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close wallpaper defaults marker: %w", closeErr)
	}
	if info != nil {
		return nil
	}
	if err := root.Mkdir("wallpapers", 0700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil
		}
		return fmt.Errorf("create wallpaper gallery: %w", err)
	}
	gallery, err := openGallery(home)
	if err != nil {
		return fmt.Errorf("open wallpaper gallery: %w", err)
	}
	defer func() { _ = gallery.Close() }()
	entries, err := bundledDefaults.ReadDir("defaults")
	if err != nil {
		return fmt.Errorf("read bundled wallpapers: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := bundledDefaults.ReadFile("defaults/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read bundled wallpaper: %w", err)
		}
		f, err := gallery.OpenFile(entry.Name(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("create wallpaper %s: %w", entry.Name(), err)
		}
		n, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr == nil && n != len(data) {
			writeErr = io.ErrShortWrite
		}
		if writeErr != nil {
			return fmt.Errorf("write wallpaper %s: %w", entry.Name(), writeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close wallpaper %s: %w", entry.Name(), closeErr)
		}
	}
	return nil
}
