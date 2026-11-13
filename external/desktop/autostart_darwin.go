//go:build desktop && darwin

package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Opening the window at login on macOS is one launch agent in the user's own
// LaunchAgents folder, which launchd reads when that account logs in. Nothing
// is installed for the whole machine and nothing needs administrator rights -
// the agent belongs to the person who switched it on, the same way the agent
// home does.

// autostartLabel is the agent's label, and the name of its file.
const autostartLabel = "dev.coddy.desktop"

// autostartPath answers where the agent lives.
func autostartPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("desktop: no home directory to put the launch agent in: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", autostartLabel+".plist"), nil
}

// setAutostart writes the agent or takes it away.
//
// The file alone is enough for the next login, which is what the switch
// promises. It is deliberately not loaded into the running launchd session:
// that would open a second window on the spot, which nobody asked for by
// ticking a box about the next login.
func setAutostart(on bool) error {
	path, err := autostartPath()
	if err != nil {
		return err
	}
	if !on {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("desktop: removing the launch agent: %w", err)
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("desktop: the launch agent needs this program's own path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("desktop: the LaunchAgents folder: %w", err)
	}
	if err := os.WriteFile(path, []byte(autostartAgent(exe)), 0o600); err != nil {
		return fmt.Errorf("desktop: writing the launch agent: %w", err)
	}
	return nil
}

// autostartEnabled reports whether the agent is there.
func autostartEnabled() bool {
	path, err := autostartPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// autostartAgent is the property list itself: this program, with the desktop
// verb, run once at login.
func autostartAgent(exe string) string {
	return strings.Join([]string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">`,
		`<plist version="1.0">`,
		`<dict>`,
		`	<key>Label</key>`,
		`	<string>` + plistEscape(autostartLabel) + `</string>`,
		`	<key>ProgramArguments</key>`,
		`	<array>`,
		`		<string>` + plistEscape(exe) + `</string>`,
		`		<string>desktop</string>`,
		`	</array>`,
		`	<key>RunAtLoad</key>`,
		`	<true/>`,
		// Interactive: the window belongs to the person logging in, and
		// launchd must not throttle it the way it does a background job.
		`	<key>ProcessType</key>`,
		`	<string>Interactive</string>`,
		`</dict>`,
		`</plist>`,
		``,
	}, "\n")
}

// plistEscape escapes what XML reserves, because a program installed under a
// folder with an ampersand in its name is still a program.
func plistEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
