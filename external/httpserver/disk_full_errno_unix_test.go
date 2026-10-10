//go:build http && !windows

package httpserver

import "syscall"

// diskFullErrno is the code this platform's kernel answers a write with when
// the volume is full.
const diskFullErrno = syscall.ENOSPC
