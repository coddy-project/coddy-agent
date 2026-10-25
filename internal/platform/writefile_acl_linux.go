package platform

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// posixACL is the extended attribute that holds a file's POSIX access ACL.
const posixACL = "system.posix_acl_access"

// matchAccess gives the replacement of target the target's POSIX access ACL, or
// none when the target has none. The mode alone cannot carry it: with an ACL the
// group bits of the mode are the ACL's mask, so the mode without the ACL would
// hand the owning group the access the mask allows named users. And a
// replacement created under a folder's default ACL takes that ACL, which the
// target may not have.
func matchAccess(tmp, target *os.File, _ fs.FileInfo) error {
	fd, from := int(tmp.Fd()), int(target.Fd())
	size, err := unix.Fgetxattr(from, posixACL, nil)
	switch {
	case errors.Is(err, unix.ENODATA):
		if err := unix.Fremovexattr(fd, posixACL); err != nil && !errors.Is(err, unix.ENODATA) && !errors.Is(err, unix.ENOTSUP) {
			return err
		}
		return nil
	case errors.Is(err, unix.ENOTSUP):
		return nil // a file system without ACLs
	case err != nil:
		return err
	}
	acl := make([]byte, size)
	if size, err = unix.Fgetxattr(from, posixACL, acl); err != nil {
		return err
	}
	return unix.Fsetxattr(fd, posixACL, acl[:size], 0)
}
