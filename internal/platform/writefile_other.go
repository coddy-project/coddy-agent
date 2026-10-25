//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package platform

import "os"

func linkCount(string) uint64 { return 0 }

func syncDir(string) error { return nil }

func commitReplace(tmpPath, target string) error { return os.Rename(tmpPath, target) }
