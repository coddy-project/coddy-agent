//go:build http && windows

package httpserver

import "golang.org/x/sys/windows"

// diskFullErrno is the code Windows answers a write with when the volume is
// full (ERROR_DISK_FULL).
const diskFullErrno = windows.ERROR_DISK_FULL
