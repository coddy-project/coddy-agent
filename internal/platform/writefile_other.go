//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package platform

import "os"

func linkCount(string) uint64 { return 0 }

func syncDir(string) error { return nil }

func isRetryableRenameError(error) bool { return false }

func commitReplace(tmpPath, target string, _ bool) error {
	return retryReplace(func() error { return os.Rename(tmpPath, target) })
}
