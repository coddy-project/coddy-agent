//go:build !windows

package platform

import "syscall"

// diskFullErrnos are the codes a Unix kernel (Linux and Android included,
// which take the linux build tag) answers a write with when the volume is
// full (ENOSPC) or the account's quota on it is used up (EDQUOT).
var diskFullErrnos = []error{syscall.ENOSPC, syscall.EDQUOT}
