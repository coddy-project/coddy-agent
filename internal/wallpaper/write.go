package wallpaper

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// UploadResult names the stored picture, which may have existed under another name.
type UploadResult struct {
	Wallpaper Entry `json:"wallpaper"`
	Created   bool  `json:"created"`
}

// Upload streams an original into the local gallery without a size or geometry
// limit. Equal SHA-256 digests reuse an existing picture; name collisions never
// overwrite a file. Only header recognition, not pixel decoding, runs here.
func Upload(ctx context.Context, home, name string, body io.Reader) (result UploadResult, err error) {
	if !validName(name) {
		return result, ErrInvalidName
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	root, err := openGalleryMode(home, true)
	if err != nil {
		return result, err
	}
	defer func() { _ = root.Close() }()
	file, temp, err := createTemporary(ctx, root)
	if err != nil {
		return result, err
	}
	defer func() {
		if file != nil {
			if closeErr := file.Close(); closeErr != nil {
				err = errors.Join(err, ErrUnavailable)
			}
		}
		if removeErr := root.Remove(temp); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			err = errors.Join(err, ErrUnavailable)
		}
	}()
	digest := sha256.New()
	if _, err := io.Copy(io.MultiWriter(file, digest), contextReader{ctx, body}); err != nil {
		return result, mutationError(ctx)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return result, ErrUnavailable
	}
	if _, err := recognize(contextReader{ctx, file}); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, err
	}
	info, err := file.Stat()
	if err != nil {
		return result, ErrUnavailable
	}
	if err := file.Close(); err != nil {
		file = nil
		return result, ErrUnavailable
	}
	file = nil
	if err := lockMutations(ctx); err != nil {
		return result, err
	}
	defer unlockMutations()
	var sum [sha256.Size]byte
	copy(sum[:], digest.Sum(nil))
	existing, err := findDuplicate(ctx, root, sum)
	if err != nil {
		return result, err
	}
	if existing != "" {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		return UploadResult{Wallpaper: Entry{Name: existing}}, nil
	}
	stored, err := publish(ctx, root, temp, name, info)
	if err != nil {
		return result, err
	}
	return UploadResult{Wallpaper: Entry{Name: stored}, Created: true}, nil
}

func createTemporary(ctx context.Context, root *os.Root) (*os.File, string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		name := ".upload-" + rand.Text()
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", ErrUnavailable
		}
		return file, name, nil
	}
}

func findDuplicate(ctx context.Context, root *os.Root, sum [sha256.Size]byte) (string, error) {
	dir, err := root.Open(".")
	if err != nil {
		return "", ErrUnavailable
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if readErr != nil || closeErr != nil {
		return "", ErrUnavailable
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if !validName(entry.Name()) {
			continue
		}
		equal, err := matchesDigest(ctx, root, entry.Name(), sum)
		if err != nil {
			return "", err
		}
		if equal {
			return entry.Name(), nil
		}
	}
	return "", nil
}

func matchesDigest(ctx context.Context, root *os.Root, name string, sum [sha256.Size]byte) (bool, error) {
	picture, err := readImageContext(ctx, root, name)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidImage) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	digest := sha256.New()
	_, readErr := io.Copy(digest, contextReader{ctx, picture.File})
	closeErr := picture.File.Close()
	if readErr != nil || closeErr != nil {
		return false, mutationError(ctx)
	}
	var actual [sha256.Size]byte
	copy(actual[:], digest.Sum(nil))
	return actual == sum, nil
}

func publish(ctx context.Context, root *os.Root, temp, requested string, info fs.FileInfo) (string, error) {
	ext := filepath.Ext(requested)
	stem := strings.TrimSuffix(requested, ext)
	for suffix := 1; ; suffix++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		name := requested
		if suffix > 1 {
			name = fmt.Sprintf("%s-%d%s", stem, suffix, ext)
		}
		if _, err := root.Lstat(name); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", ErrUnavailable
		}
		if err := checkIdentity(root, temp, info); err != nil {
			return "", err
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// Link is atomic and refuses an occupied destination. Rename would
		// replace it on Unix, including files created by another process.
		if err := root.Link(temp, name); err != nil {
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			return "", ErrUnavailable
		}
		return name, nil
	}
}
