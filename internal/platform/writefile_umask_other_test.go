//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package platform

func setUmask(int) int { return 0 }
