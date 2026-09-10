package platform

import (
	"errors"
	"testing"
)

func lookPathFor(present ...string) func(string) (string, error) {
	set := map[string]bool{}
	for _, p := range present {
		set[p] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
}

func envFrom(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

func TestLocalBrowserAvailable(t *testing.T) {
	cases := []struct {
		name    string
		goos    string
		env     map[string]string
		openers []string
		want    bool
	}{
		{
			name:    "linux desktop with an opener",
			goos:    "linux",
			env:     map[string]string{"DISPLAY": ":0"},
			openers: []string{"xdg-open"},
			want:    true,
		},
		{
			name:    "wayland desktop",
			goos:    "linux",
			env:     map[string]string{"WAYLAND_DISPLAY": "wayland-0"},
			openers: []string{"gio"},
			want:    true,
		},
		{
			name:    "server with no display",
			goos:    "linux",
			env:     map[string]string{},
			openers: []string{"xdg-open"},
			want:    false,
		},
		{
			name:    "display but nothing to open it with",
			goos:    "linux",
			env:     map[string]string{"DISPLAY": ":0"},
			openers: nil,
			want:    false,
		},
		{
			// X11 forwarding sets DISPLAY on the far machine; the browser it
			// would raise (if any) is not the one the user is looking at.
			name:    "ssh session with a forwarded display",
			goos:    "linux",
			env:     map[string]string{"DISPLAY": "localhost:10.0", "SSH_CONNECTION": "1.2.3.4 22 5.6.7.8 22"},
			openers: []string{"xdg-open"},
			want:    false,
		},
		{
			name: "macOS reached over ssh",
			goos: "darwin",
			env:  map[string]string{"SSH_TTY": "/dev/ttys001"},
			want: false,
		},
		{
			name: "macOS at the console",
			goos: "darwin",
			env:  map[string]string{},
			want: true,
		},
		{
			name: "windows at the console",
			goos: "windows",
			env:  map[string]string{},
			want: true,
		},
		{
			name:    "WSL where only wslview exists",
			goos:    "linux",
			env:     map[string]string{"DISPLAY": ":0"},
			openers: []string{"wslview"},
			want:    true,
		},
		{
			name:    "NO_BROWSER wins over a working desktop",
			goos:    "linux",
			env:     map[string]string{"DISPLAY": ":0", "NO_BROWSER": "1"},
			openers: []string{"xdg-open"},
			want:    false,
		},
		{
			name:    "BROWSER=none wins over a working desktop",
			goos:    "linux",
			env:     map[string]string{"DISPLAY": ":0", "BROWSER": "none"},
			openers: []string{"xdg-open"},
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := localBrowserAvailable(tc.goos, envFrom(tc.env), lookPathFor(tc.openers...))
			if got != tc.want {
				t.Fatalf("localBrowserAvailable() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBrowserOpenArgv(t *testing.T) {
	cases := []struct {
		name    string
		goos    string
		env     map[string]string
		openers []string
		want    []string
	}{
		{
			name: "macOS",
			goos: "darwin",
			want: []string{"open", "https://hub/device"},
		},
		{
			name: "windows",
			goos: "windows",
			want: []string{"rundll32", "url.dll,FileProtocolHandler", "https://hub/device"},
		},
		{
			name:    "xdg-open takes the URL directly",
			goos:    "linux",
			openers: []string{"xdg-open"},
			want:    []string{"xdg-open", "https://hub/device"},
		},
		{
			// Bare `gio <url>` only prints its usage, and the failure is
			// invisible: the caller never waits on the child.
			name:    "gio needs its subcommand",
			goos:    "linux",
			openers: []string{"gio"},
			want:    []string{"gio", "open", "https://hub/device"},
		},
		{
			name:    "WSL falls back to wslview",
			goos:    "linux",
			openers: []string{"wslview"},
			want:    []string{"wslview", "https://hub/device"},
		},
		{
			name:    "BROWSER names the command",
			goos:    "linux",
			env:     map[string]string{"BROWSER": "firefox"},
			openers: []string{"xdg-open"},
			want:    []string{"firefox", "https://hub/device"},
		},
		{
			// A template or a list is more convention than practice; fall back
			// to the openers rather than running something malformed.
			name:    "BROWSER template is ignored",
			goos:    "linux",
			env:     map[string]string{"BROWSER": "firefox %s"},
			openers: []string{"xdg-open"},
			want:    []string{"xdg-open", "https://hub/device"},
		},
		{
			name:    "nothing to open with",
			goos:    "linux",
			openers: nil,
			want:    nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := browserOpenArgv(tc.goos, envFrom(tc.env), lookPathFor(tc.openers...), "https://hub/device")
			if len(got) != len(tc.want) {
				t.Fatalf("browserOpenArgv() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("browserOpenArgv() = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestLocalBrowserAvailableHonoursBROWSER(t *testing.T) {
	// A named browser is a browser even when none of the openers is installed.
	env := map[string]string{"DISPLAY": ":0", "BROWSER": "firefox"}
	if !localBrowserAvailable("linux", envFrom(env), lookPathFor()) {
		t.Fatal("BROWSER=firefox must read as a browser being available")
	}
}
