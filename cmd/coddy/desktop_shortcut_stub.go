//go:build desktop && http && ui && !((linux && !android) || windows)

package main

import "flag"

func desktopShortcutFlag(*flag.FlagSet) *bool { return new(bool) }

func installDesktopShortcut() error { return nil }

func maybeMigrateDesktopShortcut() {}
