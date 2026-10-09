package platform

import "errors"

// IsDiskFull reports whether err, or any error it wraps, says the volume a
// write went to has no room left: ENOSPC and EDQUOT on Unix, ERROR_DISK_FULL
// and ERROR_HANDLE_DISK_FULL on Windows. It looks through *os.PathError,
// *os.LinkError, *os.SyscallError, fmt.Errorf %w chains and errors.Join, so a
// caller asks the error it got back from a store without knowing how many
// layers wrapped it. A plain error that merely says "no space left" in its
// text is not one: the check is on the code the operating system returned.
func IsDiskFull(err error) bool {
	if err == nil {
		return false
	}
	for _, code := range diskFullErrnos {
		if errors.Is(err, code) {
			return true
		}
	}
	return false
}
