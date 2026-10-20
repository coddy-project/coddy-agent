//go:build unix

package gitws

import (
	"fmt"
	"os"
	"syscall"
)

// openRegularFlags open a file for reading without waiting and without
// following a link: O_NOFOLLOW refuses a link and O_NONBLOCK keeps a FIFO
// swapped in after a check from blocking the open.
const openRegularFlags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK

// openRegular opens path for reading only when it is a regular file, deciding
// on the opened descriptor rather than on an earlier look, so a pointer file
// replaced under the lookup never holds it.
func openRegular(path string) (*os.File, error) {
	f, err := os.OpenFile(path, openRegularFlags, 0)
	if err != nil {
		return nil, err
	}
	return regularOrClose(f, path)
}

// openRegularIn is openRegular for name inside root: the same flags and the
// same check of the opened file, and nothing outside root is reached.
func openRegularIn(root *os.Root, name string) (*os.File, error) {
	f, err := root.OpenFile(name, openRegularFlags, 0)
	if err != nil {
		return nil, err
	}
	return regularOrClose(f, name)
}

// regularOrClose returns f when it is a regular file and closes it otherwise.
func regularOrClose(f *os.File, name string) (*os.File, error) {
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	return f, nil
}
