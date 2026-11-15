//go:build linux

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
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err != nil {
			checkErr = err
			return
		}
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
