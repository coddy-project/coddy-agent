//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package platform

import "syscall"

func setUmask(mask int) int { return syscall.Umask(mask) }
