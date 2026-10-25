//go:build !linux && !windows

package platform

import "os"

// matchAccess has nothing to copy beyond the mode here: ACLs other than
// Linux's are not carried over.
func matchAccess(*os.File, string) error { return nil }
