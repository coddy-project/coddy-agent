//go:build desktop

package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The profile folder is named after the browser, so the second launch meets
// the first window's profile and is handed over to it. Anything a file name
// cannot hold is replaced rather than dropped, so two browsers never collapse
// into one folder.
func TestProfileNameIsOneFolderPerBrowser(t *testing.T) {
	cases := []struct {
		name string
		cmd  BrowserCommand
		want string
	}{
		{"a name on PATH", BrowserCommand{Path: "chromium"}, "chromium"},
		{"a path", BrowserCommand{Path: filepath.Join("/usr", "bin", "google-chrome-stable")}, "google-chrome-stable"},
		{"an explicit name wins", BrowserCommand{Path: "/opt/weird/bin/x", Name: "Yandex Browser"}, "yandex-browser"},
		{"punctuation becomes a separator", BrowserCommand{Path: "", Name: "Brave.Beta"}, "brave-beta"},
		{"nothing nameable", BrowserCommand{Path: "", Name: "..."}, "browser"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cmd.profileName(); got != c.want {
				t.Fatalf("profileName() = %q, want %q", got, c.want)
			}
		})
	}
}

// --user-data-dir is what makes the window a process of its own, so that the
// run can wait on it and the agent's cookie stays out of the browser the
// operator reads mail in. A window started without it is a bug, not a detail.
func TestWindowArgsCarryTheAppURLAndItsOwnProfile(t *testing.T) {
	args := browserArgs("http://127.0.0.1:41234/desktop/", filepath.Join("home", "desktop", "profiles", "chromium"))
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--app=http://127.0.0.1:41234/desktop/",
		"--user-data-dir=" + filepath.Join("home", "desktop", "profiles", "chromium"),
		"--no-first-run",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the window was opened with %v, which does not carry %q", args, want)
		}
	}
}

// A fresh profile is prepared before its first window; one the operator has
// already used is theirs, and nothing of it is rewritten.
func TestOnlyAFreshProfileIsSeeded(t *testing.T) {
	profile := t.TempDir()
	if err := seedProfile(profile, "http://127.0.0.1:41234"); err != nil {
		t.Fatalf("seedProfile: %v", err)
	}
	prefs := filepath.Join(profile, "Default", "Preferences")
	seeded, err := os.ReadFile(prefs)
	if err != nil {
		t.Fatalf("a fresh profile was not prepared: %v", err)
	}
	if !strings.Contains(string(seeded), "translate") {
		t.Fatalf("the seeded preferences %q do not turn the translate bubble off", seeded)
	}

	theirs := []byte(`{"translate":{"enabled":true},"theirs":1}`)
	if err := os.WriteFile(prefs, theirs, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedProfile(profile, "http://127.0.0.1:41234"); err != nil {
		t.Fatalf("seedProfile over an existing profile: %v", err)
	}
	after, err := os.ReadFile(prefs)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(theirs) {
		t.Fatalf("the operator's own preferences were rewritten as %q", after)
	}
}

// The web UI raises system notifications (Settings - Appearance -
// Notifications), and the switch that turns them on asks the browser for the
// permission. In an application window that question is Chromium's own bubble
// over the tab strip, with no address bar to explain where it came from, so a
// fresh profile is told the answer before its first window. The grant names
// this window's origin and nothing else: the profile is the launcher's, but it
// is still a browser.
func TestAFreshProfileGrantsNotificationsToTheWindowsOrigin(t *testing.T) {
	profile := t.TempDir()
	if err := seedProfile(profile, "http://127.0.0.1:41234"); err != nil {
		t.Fatalf("seedProfile: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(profile, "Default", "Preferences"))
	if err != nil {
		t.Fatalf("a fresh profile was not prepared: %v", err)
	}
	var prefs struct {
		Profile struct {
			ContentSettings struct {
				Exceptions struct {
					Notifications map[string]struct {
						Setting int `json:"setting"`
					} `json:"notifications"`
				} `json:"exceptions"`
			} `json:"content_settings"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(raw, &prefs); err != nil {
		t.Fatalf("the seeded preferences %q are not JSON Chromium reads: %v", raw, err)
	}
	granted := prefs.Profile.ContentSettings.Exceptions.Notifications
	if len(granted) != 1 {
		t.Fatalf("the seeded preferences grant notifications to %v, not to this window's origin alone", granted)
	}
	// Chromium keys a content setting by a pair of patterns, the embedder's "*".
	entry, ok := granted["http://127.0.0.1:41234,*"]
	if !ok {
		t.Fatalf("the seeded preferences grant notifications to %v, not to http://127.0.0.1:41234", granted)
	}
	if entry.Setting != contentSettingAllow {
		t.Fatalf("notifications are set to %d for this window's origin, not allowed (%d)", entry.Setting, contentSettingAllow)
	}
}

// A grant is keyed by an origin, so an address that yields nothing must yield
// nothing: a key of ",*" would be a pattern, and a pattern that broad grants
// the permission to every page the profile ever opens.
func TestTheGrantedOriginIsTheWindowsAndNothingWider(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:41234/desktop/": "http://127.0.0.1:41234",
		"http://127.0.0.1:41234":          "http://127.0.0.1:41234",
		"https://example.test/a?b=c#d":    "https://example.test",
		"/desktop/":                       "",
		"":                                "",
		"::not a url":                     "",
	}
	for addr, want := range cases {
		if got := windowOrigin(addr); got != want {
			t.Fatalf("windowOrigin(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestFindBrowserPrefersWhatTheCallerNamed(t *testing.T) {
	named := BrowserCommand{Path: "/opt/a/browser", Name: "a"}
	got, err := findBrowser(named)
	if err != nil {
		t.Fatalf("findBrowser: %v", err)
	}
	if got.Path != named.Path {
		t.Fatalf("findBrowser took %q, not the one it was given, %q", got.Path, named.Path)
	}
}

// The environment variable is the answer on a platform the window is not
// ported to and on a machine whose browser lives where nothing looks, so it is
// read before the candidate list and an unusable value is an error rather than
// a silent fall-through to the search.
func TestFindBrowserReadsTheEnvironmentAndRefusesWhatIsNotThere(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "my-browser")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(BrowserEnvVar, exe)
	got, err := findBrowser(BrowserCommand{})
	if err != nil {
		t.Fatalf("findBrowser: %v", err)
	}
	if got.Path != exe {
		t.Fatalf("findBrowser took %q, not the browser %s names, %q", got.Path, BrowserEnvVar, exe)
	}

	t.Setenv(BrowserEnvVar, filepath.Join(t.TempDir(), "nothing-here"))
	if _, err := findBrowser(BrowserCommand{}); err == nil {
		t.Fatalf("a %s that names nothing was accepted", BrowserEnvVar)
	}
}

// On a platform the window is not ported to, the failure is still the one
// `auto` is allowed to fall through on, so the run serves with its address
// printed instead of ending. The message names the platform, because "no
// browser found" would be wrong on a macOS machine with Chrome installed.
func TestAnUnportedPlatformFallsThroughAsNoBrowser(t *testing.T) {
	if len(browserCandidates()) > 0 {
		t.Skipf("%s is ported", runtime.GOOS)
	}
	t.Setenv(BrowserEnvVar, "")
	_, err := findBrowser(BrowserCommand{})
	if !errors.Is(err, errNoBrowser) {
		t.Fatalf("findBrowser answered %v, which auto cannot fall through on", err)
	}
	if !strings.Contains(err.Error(), runtime.GOOS) {
		t.Fatalf("the message %q does not say which platform it is", err)
	}
}
