//go:build !windows

package localipc

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryLock(f *os.File) (bool, error) {
	if err := f.Chmod(0o600); err != nil {
		return false, err
	}
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func unlock(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
