//go:build !linux

package platform

import (
	"net"
	"time"
)

// SetTCPUserTimeout is the stub of the platforms without TCP_USER_TIMEOUT
// (macOS, Windows, the BSDs): it changes nothing and says so. TCP_RXT_CONNDROPTIME
// and TCP_MAXRT are the closest counterparts there and are not used yet; a
// follow-up can fill this file behind the same signature.
func SetTCPUserTimeout(net.Conn, time.Duration) error {
	return ErrUserTimeoutUnsupported
}

// TCPUserTimeout is the stub twin of SetTCPUserTimeout.
func TCPUserTimeout(net.Conn) (time.Duration, error) {
	return 0, ErrUserTimeoutUnsupported
}
