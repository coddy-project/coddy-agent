//go:build windows

package platform

import "golang.org/x/sys/windows"

// diskFullErrnos are the codes Windows answers a write with when the volume
// is full: ERROR_DISK_FULL (112), ERROR_HANDLE_DISK_FULL (39) for a write
// through a handle whose volume ran out of room, and ERROR_DISK_QUOTA_EXCEEDED
// (1295) for an account whose quota on the volume is used up, the Unix EDQUOT.
// Go's own ENOSPC is an invented value no Windows API returns, so it is not
// listed.
var diskFullErrnos = []error{windows.ERROR_DISK_FULL, windows.ERROR_HANDLE_DISK_FULL, windows.ERROR_DISK_QUOTA_EXCEEDED}
