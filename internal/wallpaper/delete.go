package wallpaper

import (
	"context"
	"errors"
	"io/fs"
	"os"
)

// Delete removes only a named regular picture with a recognized raster header.
// Confirmation is the client's responsibility; this is the authenticated write.
func Delete(ctx context.Context, home, name string) error {
	if !validName(name) {
		return ErrInvalidName
	}
	if err := lockMutations(ctx); err != nil {
		return err
	}
	defer unlockMutations()
	root, err := openGallery(home)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	picture, err := readImageContext(ctx, root, name)
	if err != nil {
		return err
	}
	info, statErr := picture.File.Stat()
	// Close before removing the directory entry: Windows does not share
	// delete access with the read handle opened by os.Root.
	closeErr := picture.File.Close()
	if statErr != nil || closeErr != nil {
		return ErrUnavailable
	}
	if err := checkIdentity(root, name, info); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Remove(name); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
		return ErrUnavailable
	}
	return nil
}

func checkIdentity(root *os.Root, name string, want fs.FileInfo) error {
	current, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return ErrUnavailable
	}
	if !current.Mode().IsRegular() || !os.SameFile(want, current) {
		return ErrNotFound
	}
	return nil
}
