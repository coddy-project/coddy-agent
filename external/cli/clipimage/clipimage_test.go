//go:build cli

package clipimage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/bmp"
)

func pngBytes(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type fakeRun struct {
	answers map[string][]byte
	fail    map[string]error
	calls   []string
}

func (f *fakeRun) run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, key)
	if err, ok := f.fail[key]; ok {
		return nil, err
	}
	if out, ok := f.answers[key]; ok {
		return out, nil
	}
	return nil, errors.New("exit status 1")
}

type exitErr int

func (e exitErr) Error() string { return "exit status" }
func (e exitErr) ExitCode() int { return int(e) }

func env(goos string, vars map[string]string, bins ...string) Env {
	return Env{
		GOOS:   goos,
		Getenv: func(k string) string { return vars[k] },
		LookPath: func(b string) (string, error) {
			for _, have := range bins {
				if have == b {
					return "/usr/bin/" + b, nil
				}
			}
			return "", errors.New("not found")
		},
	}
}

func names(s []source) string {
	var out []string
	for _, x := range s {
		out = append(out, x.name)
	}
	return strings.Join(out, ",")
}

func TestPlanPicksTheDesktopsClipboard(t *testing.T) {
	x11 := func(context.Context, string) (Image, error) { return Image{}, ErrNoImage }
	cases := []struct {
		name string
		env  Env
		want string
	}{
		{"x11", env("linux", map[string]string{"DISPLAY": ":0"}), "x11"},
		{"wayland with xwayland", env("linux", map[string]string{"DISPLAY": ":0", "WAYLAND_DISPLAY": "wayland-0"}, "wl-paste"), "wl-paste,x11"},
		{"wayland without wl-paste", env("linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}), ""},
		{"no desktop", env("linux", nil), ""},
		{"freebsd x11", env("freebsd", map[string]string{"DISPLAY": ":1"}), "x11"},
		{"macos", env("darwin", nil), "osascript"},
		{"windows", env("windows", nil), "powershell"},
	}
	for _, c := range cases {
		c.env.X11 = x11
		if got := names(plan(c.env)); got != c.want {
			t.Errorf("%s: plan %q, want %q", c.name, got, c.want)
		}
	}
	if _, err := Read(context.Background(), env("linux", nil)); err == nil || errors.Is(err, ErrNoImage) {
		t.Fatalf("no clipboard at all should say so, got %v", err)
	}
}

func TestReadFallsBackOnlyWhenASourceFails(t *testing.T) {
	want := pngBytes(t, 3, 2)
	e := env("linux", map[string]string{"DISPLAY": ":0", "WAYLAND_DISPLAY": "w"}, "wl-paste")
	f := &fakeRun{fail: map[string]error{"wl-paste --list-types": errors.New("broken pipe")}}
	e.Run = f.run
	x11Asked := false
	e.X11 = func(context.Context, string) (Image, error) { x11Asked = true; return fromBytes(want, "") }
	// wl-paste exiting non-zero reads as an empty clipboard, so it never
	// reaches the X source.
	if _, err := Read(context.Background(), e); !errors.Is(err, ErrNoImage) || x11Asked {
		t.Fatalf("an empty Wayland clipboard: err %v, x11 asked %v", err, x11Asked)
	}
	e.LookPath = func(string) (string, error) { return "", errors.New("none") }
	img, err := Read(context.Background(), e)
	if err != nil || !bytes.Equal(img.Data, want) {
		t.Fatalf("x11 source: %v", err)
	}
}

func TestReadWaylandTakesTheBestPicture(t *testing.T) {
	data := pngBytes(t, 4, 4)
	f := &fakeRun{answers: map[string][]byte{
		"wl-paste --list-types":                  []byte("text/plain\nimage/jpeg\nimage/png\n"),
		"wl-paste --no-newline --type image/png": data,
	}}
	img, err := readWayland(context.Background(), f.run)
	if err != nil {
		t.Fatal(err)
	}
	if img.MIME != "image/png" || !bytes.Equal(img.Data, data) || img.Picture.Bounds().Dx() != 4 {
		t.Fatalf("got %s %d bytes", img.MIME, len(img.Data))
	}
	f = &fakeRun{answers: map[string][]byte{"wl-paste --list-types": []byte("text/plain\nUTF8_STRING\n")}}
	if _, err := readWayland(context.Background(), f.run); !errors.Is(err, ErrNoImage) {
		t.Fatalf("text only: %v", err)
	}
}

func TestACopiedFileIsReadFromItsPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot one.png")
	if err := os.WriteFile(path, pngBytes(t, 5, 5), 0o600); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(dir, "notes.txt")
	_ = os.WriteFile(notes, []byte("hi"), 0o600)
	uri := "file://" + filepath.ToSlash(path)
	if !strings.HasPrefix(filepath.ToSlash(path), "/") {
		uri = "file:///" + filepath.ToSlash(path)
	}
	uri = strings.ReplaceAll(uri, " ", "%20")
	list := "# copied\nfile://" + filepath.ToSlash(notes) + "\n" + uri + "\n"
	f := &fakeRun{answers: map[string][]byte{
		"wl-paste --list-types":                      []byte("text/uri-list\n"),
		"wl-paste --no-newline --type text/uri-list": []byte(list),
	}}
	img, err := readWayland(context.Background(), f.run)
	if filepath.Separator != '/' {
		// A file URI of a drive path does not round-trip through url.Parse
		// the same way; the Windows reader names its file directly.
		img, err = parseWindows("FILE:" + path)
	}
	if err != nil {
		t.Fatal(err)
	}
	if img.Name != "shot one.png" || img.MIME != "image/png" {
		t.Fatalf("got name %q mime %q", img.Name, img.MIME)
	}
}

func TestMacReaderParsesAppleScriptData(t *testing.T) {
	data := pngBytes(t, 2, 2)
	out := "«data PNGf" + strings.ToUpper(hex.EncodeToString(data)) + "»\n"
	got, ok := parseAppleData(out)
	if !ok || !bytes.Equal(got, data) {
		t.Fatal("the PNGf form did not decode")
	}
	if _, ok := parseAppleData("missing value"); ok {
		t.Fatal("plain text decoded as data")
	}
	f := &fakeRun{answers: map[string][]byte{"osascript -e " + macPNG: []byte(out)}}
	img, err := readMac(context.Background(), f.run)
	if err != nil || img.MIME != "image/png" {
		t.Fatalf("readMac: %v %s", err, img.MIME)
	}
	f = &fakeRun{}
	if _, err := readMac(context.Background(), f.run); !errors.Is(err, ErrNoImage) {
		t.Fatalf("an empty pasteboard: %v", err)
	}
}

func TestWindowsReader(t *testing.T) {
	data := pngBytes(t, 2, 3)
	img, err := parseWindows(base64.StdEncoding.EncodeToString(data) + "\r\n")
	if err != nil || img.Picture.Bounds().Dy() != 3 {
		t.Fatalf("base64 PNG: %v", err)
	}
	f := &fakeRun{fail: map[string]error{}}
	f.fail["powershell -NoProfile -NonInteractive -STA -Command "+windowsScript] = exitErr(3)
	if _, err := readWindows(context.Background(), f.run); !errors.Is(err, ErrNoImage) {
		t.Fatalf("exit 3 is an empty clipboard, got %v", err)
	}
}

func TestFormatsAModelDoesNotTakeBecomePNG(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 3))
	src.Set(1, 1, color.RGBA{0xff, 0, 0, 0xff})
	var buf bytes.Buffer
	if err := bmp.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	img, err := fromBytes(buf.Bytes(), "pic.bmp")
	if err != nil {
		t.Fatal(err)
	}
	if img.MIME != "image/png" || img.Name != "pic.png" {
		t.Fatalf("bmp became %s named %q", img.MIME, img.Name)
	}
	if _, err := png.Decode(bytes.NewReader(img.Data)); err != nil {
		t.Fatalf("the converted data is not PNG: %v", err)
	}
	if _, err := fromBytes([]byte("not a picture"), ""); err == nil || errors.Is(err, ErrNoImage) {
		t.Fatalf("garbage: %v", err)
	}
	if _, err := fromBytes(nil, ""); !errors.Is(err, ErrNoImage) {
		t.Fatalf("empty: %v", err)
	}
}
