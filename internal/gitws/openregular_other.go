//go:build !unix

package gitws

import (
	"fmt"
	"os"
)

// openRegular opens path for reading only when it is a regular file. Outside
// Unix there are no FIFOs in the file namespace to swap in, so a look before
// the open and one on the opened file are enough.
func openRegular(path string) (*os.File, error) {
	if fi, err := os.Lstat(path); err != nil {
		return nil, err
	} else if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
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
