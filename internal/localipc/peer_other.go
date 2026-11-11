//go:build !linux && !darwin && !windows

package localipc

import "net"

// The private socket mode and endpoint credential remain in force on Unix
// variants without a portable peer-credential implementation in x/sys.
func sameUserPeer(net.Conn) error { return nil }
