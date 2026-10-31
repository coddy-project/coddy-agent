package wallpaper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func encodePicture(t *testing.T, format string, width, height int) []byte {
	t.Helper()
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var err error
	switch format {
	case "png":
		err = png.Encode(&b, img)
	case "jpeg":
		err = jpeg.Encode(&b, img, nil)
	case "gif":
		err = gif.Encode(&b, img, nil)
	default:
		t.Fatal("unknown test image format")
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func writePicture(t *testing.T, home, name string, data []byte) {
	t.Helper()
	dir := filepath.Join(home, "wallpapers")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestListAndRead(t *testing.T) {
	home := t.TempDir()
	pngBytes := encodePicture(t, "png", 1280, 720)
	writePicture(t, home, "Zebra.png", pngBytes)
	writePicture(t, home, "Alps with snow.jpg", encodePicture(t, "jpeg", 1280, 720))
	writePicture(t, home, "blue.gif", encodePicture(t, "gif", 1280, 720))
	webpBytes, err := os.ReadFile("testdata/solid.webp")
	if err != nil {
		t.Fatal(err)
	}
	writePicture(t, home, "white.webp", webpBytes)
	writePicture(t, home, ".hidden.png", pngBytes)
	writePicture(t, home, "small.png", encodePicture(t, "png", 1279, 720))
	writePicture(t, home, "bad.png", []byte("not an image"))
	if err := os.Mkdir(filepath.Join(home, "wallpapers", "folder.png"), 0o700); err != nil {
		t.Fatal(err)
	}
	rows, err := List(home)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{{Name: "Alps with snow.jpg"}, {Name: "Zebra.png"}, {Name: "blue.gif"}, {Name: "white.webp"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %#v, want %#v", rows, want)
	}
	img, err := Read(home, "Zebra.png")
	if err != nil {
		t.Fatal(err)
	}
	if img.ContentType != "image/png" || !bytes.Equal(img.Bytes, pngBytes) {
		t.Fatal("read did not return original PNG")
	}
	for _, tc := range []struct{ name, contentType string }{{"Alps with snow.jpg", "image/jpeg"}, {"blue.gif", "image/gif"}, {"white.webp", "image/webp"}} {
		got, err := Read(home, tc.name)
		if err != nil || got.ContentType != tc.contentType {
			t.Errorf("%s: type %s, error %v", tc.name, got.ContentType, err)
		}
	}
}

func TestWebPExtendedHeaderCannotHideOversizedPixels(t *testing.T) {
	base, err := os.ReadFile("testdata/solid.webp")
	if err != nil {
		t.Fatal(err)
	}
	// Wrap the fixture's VP8L chunk in a small VP8X canvas, then give the
	// payload different dimensions. The decoder must reject it before pixels
	// are allocated, rather than trusting the small canvas from DecodeConfig.
	raw := make([]byte, 30)
	copy(raw, "RIFF")
	copy(raw[8:], "WEBPVP8X")
	binary.LittleEndian.PutUint32(raw[16:20], 10)
	raw[24], raw[25] = 0xff, 0x04 // 1280 - 1
	raw[27], raw[28] = 0xcf, 0x02 // 720 - 1
	raw = append(raw, base[12:]...)
	binary.LittleEndian.PutUint32(raw[4:8], uint32(len(raw)-8))
	if string(raw[30:34]) != "VP8L" || raw[38] != 0x2f {
		t.Fatal("unexpected WebP fixture layout")
	}
	binary.LittleEndian.PutUint32(raw[39:43], 8191|(4095<<14))
	home := t.TempDir()
	writePicture(t, home, "bad.webp", raw)
	if _, err := Read(home, "bad.webp"); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("mismatched WebP accepted: %v", err)
	}
}

func TestPixelDimensionBoundary(t *testing.T) {
	if !validDimensions(4096, 4096) || validDimensions(4097, 4096) || validDimensions(1280, 0) {
		t.Fatal("pixel limit boundary is incorrect")
	}
}

func TestMissingDirectoryAndIOErrors(t *testing.T) {
	home := t.TempDir()
	rows, err := List(home)
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("missing: %#v, %v", rows, err)
	}
	if _, err := Read(home, "missing.png"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing read: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "wallpapers"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := List(home); err == nil {
		t.Fatal("non-directory silently became empty gallery")
	}
	if _, err := List(filepath.Join(home, "missing-home")); err == nil {
		t.Fatal("missing home silently became empty gallery")
	}
}

func TestInvalidNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", ".hidden", "a/b.png", `a\b.png`, "../x", "/absolute", "a\x00b", "C:stream", "bad\nname", "\xff.png"} {
		if _, err := Read(t.TempDir(), name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("name %q: %v", name, err)
		}
	}
}

func TestImageValidation(t *testing.T) {
	valid := encodePicture(t, "png", 1280, 720)
	largeHeader := append([]byte(nil), valid...)
	binary.BigEndian.PutUint32(largeHeader[16:20], 100000)
	binary.BigEndian.PutUint32(largeHeader[20:24], 100000)
	binary.BigEndian.PutUint32(largeHeader[29:33], crc32.ChecksumIEEE(largeHeader[12:29]))
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"minimum", valid, nil},
		{"too narrow", encodePicture(t, "png", 1279, 720), ErrInvalidImage},
		{"too short", encodePicture(t, "png", 1280, 719), ErrInvalidImage},
		{"svg", []byte(`<svg width="1920" height="1080"></svg>`), ErrInvalidImage},
		{"bad pixels", largeHeader, ErrInvalidImage},
		{"truncated body", valid[:len(valid)-20], ErrInvalidImage},
		{"empty", nil, ErrInvalidImage},
		{"exact byte limit", append(append([]byte(nil), valid...), make([]byte, MaxBytes-len(valid))...), nil},
		{"byte limit exceeded", make([]byte, MaxBytes+1), ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writePicture(t, home, "image", tc.data)
			_, err := Read(home, "image")
			if !errors.Is(err, tc.want) {
				t.Fatalf("Read: %v, want %v", err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), home) {
				t.Fatalf("path leaked: %v", err)
			}
			rows, listErr := List(home)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if (len(rows) == 1) != (tc.want == nil) {
				t.Fatalf("listed invalid image: %#v", rows)
			}
		})
	}
}

func TestSymlinksAreRefused(t *testing.T) {
	home := t.TempDir()
	pngBytes := encodePicture(t, "png", 1280, 720)
	writePicture(t, home, "ok.png", pngBytes)
	if err := os.Symlink("ok.png", filepath.Join(home, "wallpapers", "link.png")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink privilege unavailable: %v", err)
		}
		t.Fatal(err)
	}
	rows, err := List(home)
	if err != nil || !reflect.DeepEqual(rows, []Entry{{Name: "ok.png"}}) {
		t.Fatalf("symlink list: %v, %v", rows, err)
	}
	if _, err := Read(home, "link.png"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("symlink read: %v", err)
	}
	if err := os.Rename(filepath.Join(home, "wallpapers"), filepath.Join(home, "real")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(home, "wallpapers")); err != nil {
		t.Fatal(err)
	}
	if _, err := List(home); err == nil {
		t.Fatal("symlink directory accepted")
	}
	if _, err := Read(home, "ok.png"); err == nil {
		t.Fatal("read through symlink directory")
	}
}

func TestOpenedFileMustStillBeRegularAndUnlinked(t *testing.T) {
	for _, replacement := range []string{"other file", "link to same inode"} {
		t.Run(replacement, func(t *testing.T) {
			home := t.TempDir()
			writePicture(t, home, "picture", []byte("original"))
			root, err := openGallery(home)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			before, err := root.Lstat("picture")
			if err != nil {
				t.Fatal(err)
			}
			if err := root.Rename("picture", "saved"); err != nil {
				t.Fatal(err)
			}
			if replacement == "other file" {
				writePicture(t, home, "picture", []byte("replacement"))
			} else if err := os.Symlink("saved", filepath.Join(home, "wallpapers", "picture")); err != nil {
				if runtime.GOOS == "windows" {
					t.Skipf("symlink privilege unavailable: %v", err)
				}
				t.Fatal(err)
			}
			file, _, err := openChecked(root, "picture", before)
			if file != nil {
				_ = file.Close()
			}
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("substitution accepted: %v", err)
			}
		})
	}
}

func TestPermissionErrorsAreNotAnEmptyGallery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows chmod does not remove read access")
	}
	for _, target := range []string{"directory", "picture"} {
		t.Run(target, func(t *testing.T) {
			home := t.TempDir()
			writePicture(t, home, "picture", encodePicture(t, "png", 1280, 720))
			path := filepath.Join(home, "wallpapers")
			if target == "picture" {
				path = filepath.Join(path, "picture")
			}
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Chmod(path, 0o700) }()
			if target == "picture" {
				if f, err := os.Open(path); err == nil {
					_ = f.Close()
					t.Skip("process can read mode-000 files")
				}
			} else if _, err := os.ReadDir(path); err == nil {
				t.Skip("process can read mode-000 directories")
			}
			if rows, err := List(home); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("I/O error hidden: %v, %v", rows, err)
			}
		})
	}
}

func TestAnimatedGIFBudgets(t *testing.T) {
	palette := color.Palette{color.Black, color.White}
	frame := image.NewPaletted(image.Rect(0, 0, 1280, 720), palette)
	for _, tc := range []struct {
		name   string
		frames int
		valid  bool
	}{{"two frames", 2, true}, {"pixel budget", 20, false}, {"frame count budget", 257, false}} {
		t.Run(tc.name, func(t *testing.T) {
			g := &gif.GIF{Config: image.Config{ColorModel: palette, Width: 1280, Height: 720}}
			frame := frame
			if tc.name == "frame count budget" {
				frame = image.NewPaletted(image.Rect(0, 0, 1, 1), palette)
			}
			for i := 0; i < tc.frames; i++ {
				g.Image = append(g.Image, frame)
				g.Delay = append(g.Delay, 1)
			}
			var b bytes.Buffer
			if err := gif.EncodeAll(&b, g); err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			writePicture(t, home, "animation.gif", b.Bytes())
			_, err := Read(home, "animation.gif")
			if (err == nil) != tc.valid {
				t.Fatalf("Read: %v", err)
			}
		})
	}
}
