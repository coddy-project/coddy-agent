package platform

import (
	"os"

	"golang.org/x/sys/unix"
)

// posixACL is the extended attribute that holds a file's POSIX access ACL.
const posixACL = "system.posix_acl_access"

// matchAccess gives the replacement of target the target's POSIX access ACL.
// The mode alone cannot carry it: with an ACL the group bits of the mode are
// the ACL's mask, so the mode without the ACL would hand the owning group the
// access the mask allows named users.
func matchAccess(tmp *os.File, target string) error {
	size, err := unix.Getxattr(target, posixACL, nil)
	if err != nil || size == 0 {
		// No ACL (ENODATA), or a file system without them (ENOTSUP).
		return nil
	}
	acl := make([]byte, size)
	if size, err = unix.Getxattr(target, posixACL, acl); err != nil {
		return err
	}
	return unix.Fsetxattr(int(tmp.Fd()), posixACL, acl[:size], 0)
}
