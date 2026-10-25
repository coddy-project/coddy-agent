//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package platform

func linkCount(string) uint64 { return 0 }

func syncDir(string) error { return nil }

func isRetryableRenameError(error) bool { return false }
