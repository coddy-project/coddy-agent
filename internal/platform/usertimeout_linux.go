//go:build linux

package platform

import (
	"fmt"
	"math"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

// maxUserTimeoutMillis is the largest value the kernel's int option holds.
const maxUserTimeoutMillis = math.MaxInt32

// SetTCPUserTimeout sets TCP_USER_TIMEOUT on a TCP connection: the longest the
// kernel keeps data in flight unacknowledged before it aborts the connection
// with ETIMEDOUT. d == 0 restores the system default (the retransmission limit,
// about 15 minutes at the usual tcp_retries2).
//
// The option is per socket and per call of this function, which is how the
// shared-model call uses it: a heartbeat written every few seconds keeps data
// in flight, so keepalive never runs and only this option bounds a peer that
// vanished without a FIN or a RST. A positive d is rounded up to a whole
// millisecond (0 would mean the default) and clamped to what the kernel holds;
// a negative d is an error.
//
// golang.org/x/sys/unix carries the constant because syscall.TCP_USER_TIMEOUT
// is not defined on every Linux architecture. GOOS=android builds this file
// too.
func SetTCPUserTimeout(c net.Conn, d time.Duration) error {
	tc, ok := c.(*net.TCPConn)
	if !ok || tc == nil {
		return ErrNotTCPConn
	}
	if d < 0 {
		return fmt.Errorf("platform: negative TCP user timeout %v", d)
	}
	ms := userTimeoutMillis(d)
	raw, err := tc.SyscallConn()
	if err != nil {
		return fmt.Errorf("platform: set TCP user timeout: %w", err)
	}
	var serr error
	if cerr := raw.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, ms)
	}); cerr != nil {
		return fmt.Errorf("platform: set TCP user timeout: %w", cerr)
	}
	if serr != nil {
		return fmt.Errorf("platform: set TCP user timeout: %w", serr)
	}
	return nil
}

// userTimeoutMillis converts d to the kernel's unit: a positive duration is
// rounded up to a whole millisecond (so it never becomes 0, which means the
// default, nor gives the peer less time than asked for) and an enormous one is
// clamped instead of wrapping around.
func userTimeoutMillis(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	ms := d.Milliseconds()
	if d%time.Millisecond != 0 {
		ms++
	}
	if ms > maxUserTimeoutMillis {
		return maxUserTimeoutMillis
	}
	return int(ms)
}

// TCPUserTimeout reads the option back: 0 is the system default. It exists for
// tests and diagnostics.
func TCPUserTimeout(c net.Conn) (time.Duration, error) {
	tc, ok := c.(*net.TCPConn)
	if !ok || tc == nil {
		return 0, ErrNotTCPConn
	}
	raw, err := tc.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("platform: read TCP user timeout: %w", err)
	}
	var (
		ms   int
		gerr error
	)
	if cerr := raw.Control(func(fd uintptr) {
		ms, gerr = unix.GetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT)
	}); cerr != nil {
		return 0, fmt.Errorf("platform: read TCP user timeout: %w", cerr)
	}
	if gerr != nil {
		return 0, fmt.Errorf("platform: read TCP user timeout: %w", gerr)
	}
	return time.Duration(ms) * time.Millisecond, nil
}
