package platform

import "errors"

// ErrUserTimeoutUnsupported is what SetTCPUserTimeout and TCPUserTimeout return
// where the operating system has no per-socket user timeout (everything but
// Linux and Android, today). A caller treats it as "this connection is not
// probed", not as a failure: the option only shortens how long a vanished peer
// is waited for, and the system's own retransmission limit still applies.
var ErrUserTimeoutUnsupported = errors.New("platform: TCP_USER_TIMEOUT is not supported here")

// ErrNotTCPConn is returned when the connection handed to SetTCPUserTimeout or
// TCPUserTimeout is not a *net.TCPConn: a TLS wrapper, a pipe or a Unix socket
// has no such option of its own. A caller that holds a wrapper unwraps it first
// (httpx.TCPConn does so for a request).
var ErrNotTCPConn = errors.New("platform: the connection is not a TCP connection")
