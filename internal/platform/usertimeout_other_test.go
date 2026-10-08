//go:build !linux

package platform

import (
	"errors"
	"net"
	"testing"
	"time"
)

// Where the kernel has no per-socket user timeout the setter says so and
// changes nothing, whatever it is handed.
func TestUserTimeoutIsUnsupportedOffLinux(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()

	if err := SetTCPUserTimeout(a, time.Second); !errors.Is(err, ErrUserTimeoutUnsupported) {
		t.Fatalf("SetTCPUserTimeout = %v, want ErrUserTimeoutUnsupported", err)
	}
	if err := SetTCPUserTimeout(nil, 0); !errors.Is(err, ErrUserTimeoutUnsupported) {
		t.Fatalf("restoring: %v, want ErrUserTimeoutUnsupported", err)
	}
	if got, err := TCPUserTimeout(a); !errors.Is(err, ErrUserTimeoutUnsupported) || got != 0 {
		t.Fatalf("TCPUserTimeout = %v, %v, want 0 and ErrUserTimeoutUnsupported", got, err)
	}
}
