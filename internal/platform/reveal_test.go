package platform

import (
	"reflect"
	"testing"
)

func TestRevealFileArgv(t *testing.T) {
	for _, tc := range []struct {
		name string
		goos string
		path string
		want []string
	}{
		{name: "macOS selects file", goos: "darwin", path: "/workspace/report.txt", want: []string{"open", "-R", "/workspace/report.txt"}},
		{name: "Windows selects file", goos: "windows", path: `C:\temp\artifact.txt`, want: []string{"explorer.exe", `/select,C:\temp\artifact.txt`}},
		{name: "Linux opens containing folder", goos: "linux", path: "/workspace/report.txt", want: []string{"xdg-open", "/workspace"}},
		{name: "FreeBSD opens containing folder", goos: "freebsd", path: "/usr/home/pat/report.txt", want: []string{"xdg-open", "/usr/home/pat"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := revealFileArgv(tc.goos, tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("argv = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestRevealFileArgvRejectsUnsupportedPlatform(t *testing.T) {
	if _, err := revealFileArgv("plan9", "/workspace/report.txt"); err == nil {
		t.Fatal("unsupported platform was accepted")
	}
}

// The two gates RevealFile asks before it builds an argv at all. A platform
// with no file manager is refused outright; the desktops that run on a display
// server are refused when there is no display, which is every `coddy serve`
// on a machine nobody is sitting at.
func TestRevealFileGates(t *testing.T) {
	for _, tc := range []struct {
		goos            string
		supported, disp bool
	}{
		{goos: "darwin", supported: true, disp: false},
		{goos: "windows", supported: true, disp: false},
		{goos: "linux", supported: true, disp: true},
		{goos: "freebsd", supported: true, disp: true},
		{goos: "android", supported: false, disp: false},
		{goos: "plan9", supported: false, disp: false},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			if got := revealSupported(tc.goos); got != tc.supported {
				t.Errorf("revealSupported = %v, want %v", got, tc.supported)
			}
			if got := revealNeedsDisplay(tc.goos); got != tc.disp {
				t.Errorf("revealNeedsDisplay = %v, want %v", got, tc.disp)
			}
		})
	}
}
