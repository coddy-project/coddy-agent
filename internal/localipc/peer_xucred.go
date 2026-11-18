//go:build darwin || freebsd

// macOS and FreeBSD answer LOCAL_PEERCRED with a struct xucred, where Linux
// answers SO_PEERCRED with a struct ucred; the two sockets are otherwise the
// same, so one file serves both.

package localipc

import (
	"fmt"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func sameUserPeer(conn net.Conn) error {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return fmt.Errorf("%w: the local transport cannot verify its peer", ErrUnsafe)
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafe, err)
	}
	var checkErr error
	if err := raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			checkErr = err
			return
		}
		// Widened rather than narrowed: a uid is unsigned on the wire and
		// signed in the standard library, and the comparison must not depend
		// on which of the two the conversion goes through.
		if int64(cred.Uid) != int64(os.Getuid()) {
			checkErr = fmt.Errorf("peer uid %d does not match %d", cred.Uid, os.Getuid())
		}
	}); err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafe, err)
	}
	if checkErr != nil {
		return fmt.Errorf("%w: %v", ErrUnsafe, checkErr)
	}
	return nil
}
