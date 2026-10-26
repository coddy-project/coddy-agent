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
	return regularOrClose(f, lookedFile{name: path})
}

// openRegularIn is openRegular for the file looked at inside root, nothing
// outside root reached. The caller has looked at it already, as openRegular
// does here, and the file opened must be the one looked at: the root follows a
// link inside itself, so a link swapped in after the look would otherwise open
// the file it points at.
func openRegularIn(root *os.Root, looked lookedFile) (*os.File, error) {
	f, err := root.Open(looked.name)
	if err != nil {
		return nil, err
	}
	return regularOrClose(f, looked)
}

// regularOrClose returns f when it is a regular file, and the one looked at when
// the look kept what it found, and closes it otherwise.
func regularOrClose(f *os.File, looked lookedFile) (*os.File, error) {
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("%s is not a regular file", looked.name)
	}
	if looked.info != nil && !os.SameFile(looked.info, fi) {
		_ = f.Close()
		return nil, fmt.Errorf("%s was replaced between the look and the open", looked.name)
	}
	return f, nil
}
