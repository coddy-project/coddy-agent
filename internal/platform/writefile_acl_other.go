//go:build !linux && !windows

package platform

import (
	"io/fs"
	"os"
)

// matchAccess has nothing to copy beyond the mode here: ACLs other than
// Linux's are not carried over.
func matchAccess(*os.File, *os.File, fs.FileInfo) error { return nil }
