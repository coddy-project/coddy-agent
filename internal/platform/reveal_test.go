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
