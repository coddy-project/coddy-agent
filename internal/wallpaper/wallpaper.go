// Package wallpaper reads the local gallery in the agent home's wallpapers directory.
package wallpaper

import (
	"errors"
	"image"
	_ "image/gif"
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

// Entry is a gallery item. It deliberately contains no filesystem path.
type Entry struct {
	Name string `json:"name"`
}

// Image owns an open original file, positioned at its start, and its metadata.
// The caller must close File after use. Only the image header is recognized;
// decoding the pixels is the browser's responsibility.
type Image struct {
	File        *os.File
	Size        int64
	ContentType string
}

var (
	ErrInvalidName  = errors.New("invalid wallpaper name")
	ErrNotFound     = errors.New("wallpaper not found")
	ErrInvalidImage = errors.New("invalid wallpaper image")
	ErrUnavailable  = errors.New("wallpaper gallery unavailable")
)

// List returns pictures with recognized raster headers directly inside
// home/wallpapers, sorted by name.
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
		picture, err := readImage(root, entry.Name())
		if err != nil {
			if errors.Is(err, ErrUnavailable) {
				return nil, err
			}
			continue
		}
		if err := picture.File.Close(); err != nil {
			return nil, ErrUnavailable
		}
		rows = append(rows, Entry{Name: entry.Name()})
	}
	slices.SortFunc(rows, func(a, b Entry) int { return strings.Compare(a.Name, b.Name) })
	return rows, nil
}

// Read opens a regular file and recognizes its raster header without decoding
// pixels or limiting its size or geometry. The caller owns the returned file.
// Names are single visible path components on every supported platform.
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
	file, opened, err := openChecked(root, name, before)
	if err != nil {
		return Image{}, err
	}
	contentType, err := recognize(file)
	if err != nil {
		_ = file.Close()
		return Image{}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return Image{}, ErrUnavailable
	}
	return Image{File: file, Size: opened.Size(), ContentType: contentType}, nil
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

// headerReader preserves storage errors even if a decoder rewrites its error.
type headerReader struct {
	reader io.Reader
	err    error
}

func (r *headerReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
	}
	return n, err
}

func recognize(reader io.Reader) (string, error) {
	header := &headerReader{reader: reader}
	_, format, err := image.DecodeConfig(header)
	if header.err != nil {
		return "", ErrUnavailable
	}
	if err != nil {
		return "", ErrInvalidImage
	}
	switch format {
	case "png":
		return "image/png", nil
	case "jpeg":
		return "image/jpeg", nil
	case "webp":
		return "image/webp", nil
	case "gif":
		return "image/gif", nil
	default:
		return "", ErrInvalidImage
	}
}
