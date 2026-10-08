//go:build linux

package platform

import (
	"errors"
	"math"
	"net"
	"testing"
	"time"
)

// tcpPair returns the two ends of a loopback connection.
func tcpPair(t *testing.T) (client, server *net.TCPConn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, aerr := ln.Accept()
		if aerr != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	s := <-accepted
	if s == nil {
		_ = c.Close()
		t.Fatal("accept failed")
	}
	t.Cleanup(func() { _ = c.Close(); _ = s.Close() })
	return c.(*net.TCPConn), s.(*net.TCPConn)
}

// The option is set, read back as set, and restored to the system default,
// which the kernel reports as 0.
func TestSetTCPUserTimeoutSetsReadsBackAndRestores(t *testing.T) {
	_, server := tcpPair(t)

	if got, err := TCPUserTimeout(server); err != nil || got != 0 {
		t.Fatalf("a fresh connection has user timeout %v (err %v), want 0", got, err)
	}
	if err := SetTCPUserTimeout(server, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if got, err := TCPUserTimeout(server); err != nil || got != 30*time.Second {
		t.Fatalf("after set: %v (err %v), want 30s", got, err)
	}
	if err := SetTCPUserTimeout(server, 0); err != nil {
		t.Fatal(err)
	}
	if got, err := TCPUserTimeout(server); err != nil || got != 0 {
		t.Fatalf("after restore: %v (err %v), want 0", got, err)
	}
}

// Setting the option on one end leaves the other alone: it is a property of one
// socket, not of the connection.
func TestSetTCPUserTimeoutTouchesOneSocket(t *testing.T) {
	client, server := tcpPair(t)
	if err := SetTCPUserTimeout(server, 12*time.Second); err != nil {
		t.Fatal(err)
	}
	if got, _ := TCPUserTimeout(client); got != 0 {
		t.Fatalf("the peer's socket got user timeout %v, want 0", got)
	}
}

// A duration the kernel counts in milliseconds must not round a positive value
// down to 0, which would silently mean "the system default".
func TestSetTCPUserTimeoutKeepsASubMillisecondValueAboveZero(t *testing.T) {
	_, server := tcpPair(t)
	if err := SetTCPUserTimeout(server, 100*time.Microsecond); err != nil {
		t.Fatal(err)
	}
	got, err := TCPUserTimeout(server)
	if err != nil {
		t.Fatal(err)
	}
	if got != time.Millisecond {
		t.Fatalf("a 100us request became %v, want 1ms", got)
	}
}

// A positive duration is rounded UP to a whole millisecond, as the doc says: a
// value that is not a whole number of milliseconds never gives the peer less
// time than asked for, and a whole one is unchanged.
func TestUserTimeoutMillisRoundsUpToAWholeMillisecond(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want int
	}{
		{0, 0},
		{-time.Second, 0},
		{1, 1},
		{100 * time.Microsecond, 1},
		{time.Millisecond, 1},
		{1001 * time.Microsecond, 2},
		{1500 * time.Microsecond, 2},
		{2*time.Millisecond - 1, 2},
		{2 * time.Millisecond, 2},
		{30 * time.Second, 30000},
		{30*time.Second + time.Nanosecond, 30001},
		{math.MaxInt32 * time.Millisecond, math.MaxInt32},
		{math.MaxInt32*time.Millisecond + time.Nanosecond, math.MaxInt32},
		{1 << 62, math.MaxInt32},
	} {
		if got := userTimeoutMillis(tc.d); got != tc.want {
			t.Errorf("userTimeoutMillis(%v) = %d, want %d", tc.d, got, tc.want)
		}
	}
}

// The same through the socket: 1500 microseconds reads back as 2 ms and a whole
// 30 s stays 30 s.
func TestSetTCPUserTimeoutRoundsUpWhatTheKernelHolds(t *testing.T) {
	_, server := tcpPair(t)
	for _, tc := range []struct{ set, want time.Duration }{
		{1500 * time.Microsecond, 2 * time.Millisecond},
		{30 * time.Second, 30 * time.Second},
	} {
		if err := SetTCPUserTimeout(server, tc.set); err != nil {
			t.Fatal(err)
		}
		if got, err := TCPUserTimeout(server); err != nil || got != tc.want {
			t.Fatalf("a %v request read back as %v (err %v), want %v", tc.set, got, err, tc.want)
		}
	}
}

// A duration the kernel cannot hold is clamped, not wrapped into a negative
// number it would refuse or a tiny one it would obey.
func TestSetTCPUserTimeoutClampsAnHugeValue(t *testing.T) {
	_, server := tcpPair(t)
	if err := SetTCPUserTimeout(server, 1<<62); err != nil {
		t.Fatal(err)
	}
	got, err := TCPUserTimeout(server)
	if err != nil {
		t.Fatal(err)
	}
	if got <= 24*time.Hour {
		t.Fatalf("an enormous request became %v, want it clamped to the largest value, not wrapped", got)
	}
}

func TestSetTCPUserTimeoutRefusesANegativeDuration(t *testing.T) {
	_, server := tcpPair(t)
	if err := SetTCPUserTimeout(server, -time.Second); err == nil {
		t.Fatal("a negative user timeout was accepted")
	}
}

// Only a TCP socket has the option; any other connection is an error that says
// so, and is not the "unsupported platform" error callers treat as a no-op.
func TestSetTCPUserTimeoutRefusesAnythingButTCP(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()

	err := SetTCPUserTimeout(a, time.Second)
	if err == nil {
		t.Fatal("a pipe took a TCP option")
	}
	if !errors.Is(err, ErrNotTCPConn) {
		t.Fatalf("error %v does not name the connection as not TCP", err)
	}
	if errors.Is(err, ErrUserTimeoutUnsupported) {
		t.Fatalf("a non-TCP connection is not an unsupported platform: %v", err)
	}
	if _, err := TCPUserTimeout(a); !errors.Is(err, ErrNotTCPConn) {
		t.Fatalf("reading it back: %v", err)
	}
	if err := SetTCPUserTimeout(nil, time.Second); !errors.Is(err, ErrNotTCPConn) {
		t.Fatalf("a nil connection: %v", err)
	}
}

// A closed socket reports an error instead of panicking, which is what a
// deferred restore after a kernel abort meets.
func TestSetTCPUserTimeoutOnAClosedSocketIsAnError(t *testing.T) {
	_, server := tcpPair(t)
	_ = server.Close()
	if err := SetTCPUserTimeout(server, 0); err == nil {
		t.Fatal("restoring on a closed socket reported no error")
	}
}
