//go:build cli

// Package clipimage reads a picture out of the system clipboard for the
// console: what a screenshot tool or an image viewer copied, or an image
// file copied in a file manager.
//
// A terminal pastes text only, so the console asks the desktop itself:
//   - X11 (and XWayland) over the X protocol in Go, no xclip needed (x11.go);
//   - Wayland through wl-paste, from wl-clipboard;
//   - macOS through osascript, which every Mac has;
//   - Windows through PowerShell and System.Windows.Forms.
//
// The sources are tried in that order and the first picture wins; a source
// that is not there is skipped.
package clipimage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	// The formats a clipboard hands out, decoded to check and to edit.
	_ "image/gif"
	_ "image/jpeg"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// ErrNoImage says the clipboard holds no picture.
var ErrNoImage = errors.New("the clipboard holds no image")

// MaxBytes bounds what is read from the clipboard: a picture larger than
// this would not reach a model anyway.
const MaxBytes = 50 << 20

// MaxPixels bounds the size a picture may declare: a small file can claim a
// huge canvas, and decoding it, then editing it, holds four bytes a pixel
// several times over. 40 megapixels is past an 8K screenshot.
const MaxPixels = 40_000_000

// Image is a picture taken from the clipboard.
type Image struct {
	// Data is the encoded picture and MIME its type; a format a model does
	// not take (BMP, TIFF) arrives converted to PNG.
	Data []byte
	MIME string
	// Name is the file name when the picture came from a copied file.
	Name string
	// Picture is Data decoded.
	Picture image.Image
}

// Env is what reading the clipboard depends on, so tests can stand in for
// the system.
type Env struct {
	GOOS     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	// Run runs a program and returns its standard output.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// X11 reads the clipboard of an X display.
	X11 func(ctx context.Context, display string) (Image, error)
}

// Read takes the picture the clipboard holds now.
func Read(ctx context.Context, env Env) (Image, error) {
	sources := plan(env)
	if len(sources) == 0 {
		return Image{}, errors.New("no clipboard to read: no X display, no wl-paste on a Wayland session")
	}
	var firstErr error
	empty := false
	for _, s := range sources {
		img, err := s.read(ctx)
		if err == nil {
			return img, nil
		}
		if ctx.Err() != nil {
			return Image{}, ctx.Err()
		}
		// The next source is asked either way: wl-paste exits 1 for an
		// empty clipboard and for a compositor it cannot reach alike (a
		// stale WAYLAND_DISPLAY inside tmux), and X may still answer.
		if errors.Is(err, ErrNoImage) {
			empty = true
		} else if firstErr == nil {
			firstErr = err
		}
	}
	if empty {
		return Image{}, ErrNoImage
	}
	return Image{}, firstErr
}

type source struct {
	name string
	read func(ctx context.Context) (Image, error)
}

// plan lists the sources to try, best first.
func plan(env Env) []source {
	getenv := env.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	has := func(bin string) bool {
		if env.LookPath == nil {
			return false
		}
		_, err := env.LookPath(bin)
		return err == nil
	}
	switch env.GOOS {
	case "darwin":
		return []source{{"osascript", func(ctx context.Context) (Image, error) { return readMac(ctx, env.Run) }}}
	case "windows":
		return []source{{"powershell", func(ctx context.Context) (Image, error) { return readWindows(ctx, env.Run) }}}
	}
	var out []source
	if getenv("WAYLAND_DISPLAY") != "" && has("wl-paste") {
		out = append(out, source{"wl-paste", func(ctx context.Context) (Image, error) { return readWayland(ctx, env.Run) }})
	}
	if d := strings.TrimSpace(getenv("DISPLAY")); d != "" && env.X11 != nil {
		out = append(out, source{"x11", func(ctx context.Context) (Image, error) { return env.X11(ctx, d) }})
	}
	return out
}

// imageTypes are the clipboard types of a picture, best first.
var imageTypes = []string{"image/png", "image/jpeg", "image/webp", "image/gif", "image/bmp", "image/tiff"}

// pickType chooses the best picture type offered, or the file list when no
// picture is, or "".
func pickType(offered []string) string {
	set := map[string]bool{}
	for _, t := range offered {
		set[strings.ToLower(strings.TrimSpace(t))] = true
	}
	for _, t := range imageTypes {
		if set[t] {
			return t
		}
	}
	if set["text/uri-list"] {
		return "text/uri-list"
	}
	return ""
}

// fromBytes checks that data is a picture and converts what a model does not
// take to PNG.
func fromBytes(data []byte, name string) (Image, error) {
	if len(data) == 0 {
		return Image{}, ErrNoImage
	}
	if len(data) > MaxBytes {
		return Image{}, fmt.Errorf("the clipboard image is larger than %d MB", MaxBytes>>20)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("the clipboard image cannot be read: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return Image{}, fmt.Errorf("the clipboard image is %dx%d, larger than %d megapixels", cfg.Width, cfg.Height, MaxPixels/1_000_000)
	}
	pic, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("the clipboard image cannot be read: %w", err)
	}
	switch format {
	case "png", "jpeg", "gif", "webp":
		return Image{Data: data, MIME: "image/" + format, Name: name, Picture: pic}, nil
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, pic); err != nil {
		return Image{}, err
	}
	if name != "" {
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".png"
	}
	return Image{Data: buf.Bytes(), MIME: "image/png", Name: name, Picture: pic}, nil
}

// fromURIList reads the first picture file a copied file list names.
func fromURIList(list string) (Image, error) {
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		path := line
		if strings.HasPrefix(line, "file://") {
			u, err := url.Parse(line)
			if err != nil {
				continue
			}
			path = u.Path
		} else if !filepath.IsAbs(line) {
			continue
		}
		if img, err := fromFile(path); err == nil {
			return img, nil
		}
	}
	return Image{}, ErrNoImage
}

// fromFile reads a picture file.
func fromFile(path string) (Image, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tif", ".tiff":
	default:
		return Image{}, ErrNoImage
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return Image{}, ErrNoImage
	}
	if st.Size() > MaxBytes {
		return Image{}, fmt.Errorf("%s is larger than %d MB", filepath.Base(path), MaxBytes>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Image{}, err
	}
	return fromBytes(data, filepath.Base(path))
}
