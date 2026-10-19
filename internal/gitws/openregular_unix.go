//go:build unix

package gitws

import (
	"fmt"
	"os"
	"syscall"
)

// openRegular opens path for reading only when it is a regular file, deciding
// on the opened descriptor rather than on an earlier look: O_NOFOLLOW refuses
// a link and O_NONBLOCK keeps a FIFO swapped in after a check from blocking
// the open, so a pointer file replaced under the lookup never holds it.
func openRegular(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return f, nil
}
