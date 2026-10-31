// Package wallpaper reads the local gallery in the agent home's wallpapers directory.
package wallpaper

import (
	"bytes"
	"errors"
	"image"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	_ "golang.org/x/image/webp"
)

const (
	// MaxBytes bounds both the file size and the bytes read from an opened file.
	MaxBytes = 20 << 20
	// MaxPixels bounds a decoded image, or the sum of all GIF frame areas.
	MaxPixels = 16 << 20
	MinWidth  = 1280
	MinHeight = 720
)

// Entry is a gallery item. It deliberately contains no filesystem path.
type Entry struct {
	Name string `json:"name"`
}

// Image contains the validated original bytes and their detected media type.
type Image struct {
	Bytes       []byte
	ContentType string
}

var (
	ErrInvalidName  = errors.New("invalid wallpaper name")
	ErrNotFound     = errors.New("wallpaper not found")
	ErrInvalidImage = errors.New("invalid wallpaper image")
	ErrTooLarge     = errors.New("wallpaper exceeds the byte limit")
	ErrUnavailable  = errors.New("wallpaper gallery unavailable")
)

// List returns valid pictures directly inside home/wallpapers, sorted by name.
// Only a missing wallpapers directory is an empty gallery; other I/O failures
// are errors, including a directory that is a link or a non-directory.
func List(home string) ([]Entry, error) {
	rows := make([]Entry, 0)
	root, err := openGallery(home)
	if errors.Is(err, ErrNotFound) {
		return rows, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	dir, err := root.Open(".")
	if err != nil {
		return nil, ErrUnavailable
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, ErrUnavailable
	}
	for _, entry := range entries {
		if !validName(entry.Name()) {
			continue
		}
		if _, err := readImage(root, entry.Name()); err != nil {
			if errors.Is(err, ErrUnavailable) {
				return nil, err
			}
			continue
		}
		rows = append(rows, Entry{Name: entry.Name()})
	}
	slices.SortFunc(rows, func(a, b Entry) int { return strings.Compare(a.Name, b.Name) })
	return rows, nil
}

// Read opens and validates a named picture. Names are single visible path
// components on every supported platform, never an absolute or relative path.
func Read(home, name string) (Image, error) {
	if !validName(name) {
		return Image{}, ErrInvalidName
	}
	root, err := openGallery(home)
	if err != nil {
		return Image{}, err
	}
	defer func() { _ = root.Close() }()
	return readImage(root, name)
}

func validName(name string) bool {
	return name != "" && utf8.ValidString(name) && !strings.HasPrefix(name, ".") &&
		!strings.ContainsAny(name, "/\\:") && !strings.ContainsFunc(name, unicode.IsControl)
}

func openGallery(home string) (*os.Root, error) {
	// An empty configuration must not accidentally expose the process cwd.
	if home == "" {
		return nil, ErrUnavailable
	}
	parent, err := os.OpenRoot(home)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer func() { _ = parent.Close() }()
	before, err := parent.Lstat("wallpapers")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	if !before.IsDir() || before.Mode()&fs.ModeSymlink != 0 {
		return nil, ErrUnavailable
	}
	root, err := parent.OpenRoot("wallpapers")
	if err != nil {
		return nil, ErrUnavailable
	}
	opened, statErr := root.Stat(".")
	after, lookErr := parent.Lstat("wallpapers")
	if statErr != nil || lookErr != nil || !after.IsDir() || after.Mode()&fs.ModeSymlink != 0 ||
		!os.SameFile(before, opened) || !os.SameFile(before, after) {
		_ = root.Close()
		return nil, ErrUnavailable
	}
	return root, nil
}

func readImage(root *os.Root, name string) (Image, error) {
	before, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return Image{}, ErrNotFound
	}
	if err != nil {
		return Image{}, ErrUnavailable
	}
	if !before.Mode().IsRegular() {
		return Image{}, ErrNotFound
	}
	if before.Size() > MaxBytes {
		return Image{}, ErrTooLarge
	}
	file, opened, err := openChecked(root, name, before)
	if err != nil {
		return Image{}, err
	}
	defer func() { _ = file.Close() }()
	if opened.Size() > MaxBytes {
		return Image{}, ErrTooLarge
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxBytes+1))
	if err != nil {
		return Image{}, ErrUnavailable
	}
	if len(raw) > MaxBytes {
		return Image{}, ErrTooLarge
	}
	contentType, err := validate(raw)
	if err != nil {
		return Image{}, err
	}
	return Image{Bytes: raw, ContentType: contentType}, nil
}

func openChecked(root *os.Root, name string, before fs.FileInfo) (*os.File, fs.FileInfo, error) {
	file, err := root.OpenFile(name, readFlags, 0)
	if err != nil {
		// A link or special file swapped in after Lstat is not a storage failure.
		after, lookErr := root.Lstat(name)
		if errors.Is(lookErr, fs.ErrNotExist) || (lookErr == nil && (!after.Mode().IsRegular() || !os.SameFile(before, after))) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, ErrUnavailable
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, ErrUnavailable
	}
	after, lookErr := root.Lstat(name)
	if lookErr != nil {
		_ = file.Close()
		if errors.Is(lookErr, fs.ErrNotExist) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, ErrUnavailable
	}
	// os.Root confines links but may resolve them even with O_NOFOLLOW. A
	// link back to the inspected inode therefore also needs this final Lstat.
	if !opened.Mode().IsRegular() || !after.Mode().IsRegular() ||
		!os.SameFile(before, opened) || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, nil, ErrNotFound
	}
	return file, opened, nil
}

func validDimensions(w, h int) bool {
	return w >= MinWidth && h >= MinHeight && w <= MaxPixels/h
}

func validate(raw []byte) (string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || !validDimensions(cfg.Width, cfg.Height) {
		return "", ErrInvalidImage
	}
	contentType := ""
	switch format {
	case "png":
		contentType = "image/png"
	case "jpeg":
		contentType = "image/jpeg"
	case "webp":
		contentType = "image/webp"
	case "gif":
		if err := checkGIFBudget(raw); err != nil {
			return "", err
		}
		if _, err := gif.DecodeAll(bytes.NewReader(raw)); err != nil {
			return "", ErrInvalidImage
		}
		return "image/gif", nil
	default:
		return "", ErrInvalidImage
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(raw))
	if err != nil || decodedFormat != format {
		return "", ErrInvalidImage
	}
	bounds := decoded.Bounds()
	if bounds.Dx() != cfg.Width || bounds.Dy() != cfg.Height {
		return "", ErrInvalidImage
	}
	return contentType, nil
}
