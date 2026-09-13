package webauth

import (
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Throttle makes guessing a password expensive without ever locking an account.
//
// A lockout would be a way to shut the operator out of their own agent from the
// outside, so what grows here is the wait before an answer, doubling with each
// consecutive failure from one address and forgotten after a quiet window. A
// correct password clears the address immediately.
//
// Loopback is exempt: the console, the harnesses and the operator sitting at the
// machine all come from there, and a laptop that locked itself out of its own
// agent would be the feature's worst failure mode.
type Throttle struct {
	// Now is the clock, overridable in tests. Nil means time.Now.
	Now func() time.Time
	// Base is the wait after the first failure; each further failure doubles it.
	// Zero means DefaultThrottleBase.
	Base time.Duration
	// Max caps the wait. Zero means DefaultThrottleMax.
	Max time.Duration
	// Window is how long a failure is remembered. Zero means DefaultThrottleWindow.
	Window time.Duration

	mu      sync.Mutex
	entries map[string]throttleEntry
}

// Throttle defaults: a wrong password costs a quarter of a second, the tenth
// wrong password in a row costs five, and a quarter of an hour of quiet forgets
// the address.
const (
	DefaultThrottleBase   = 250 * time.Millisecond
	DefaultThrottleMax    = 5 * time.Second
	DefaultThrottleWindow = 15 * time.Minute
)

// maxThrottleEntries bounds the address table, so a spray from a wide range of
// source addresses cannot grow the process.
const maxThrottleEntries = 4096

type throttleEntry struct {
	failures int
	last     time.Time
}

func (t *Throttle) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Throttle) base() time.Duration {
	if t.Base > 0 {
		return t.Base
	}
	return DefaultThrottleBase
}

func (t *Throttle) max() time.Duration {
	if t.Max > 0 {
		return t.Max
	}
	return DefaultThrottleMax
}

func (t *Throttle) window() time.Duration {
	if t.Window > 0 {
		return t.Window
	}
	return DefaultThrottleWindow
}

// Penalize counts one attempt from addr and returns how long to wait before
// answering it.
//
// Counting first is what makes a burst cost anything: ten attempts that arrive
// together would all read a delay of zero if each waited for the one before it
// to be judged wrong. The count is undone by Reset when the password turns out
// to be right, so a correct sign-in never pays for the attempts around it.
func (t *Throttle) Penalize(addr string) time.Duration {
	host := NormalizeAddr(addr)
	if host == "" || IsLoopbackAddr(host) {
		return 0
	}
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entries == nil {
		t.entries = make(map[string]throttleEntry)
	}
	t.sweepLocked(now)
	ent := t.entries[host]
	if now.Sub(ent.last) > t.window() {
		ent.failures = 0
	}
	ent.failures++
	ent.last = now
	t.entries[host] = ent
	return t.delayForLocked(ent.failures)
}

// Delay reports what an attempt from addr would wait for right now, without
// counting it. It is what a report or a test asks; the sign-in path asks
// Penalize.
func (t *Throttle) Delay(addr string) time.Duration {
	host := NormalizeAddr(addr)
	if host == "" || IsLoopbackAddr(host) {
		return 0
	}
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	ent, ok := t.entries[host]
	if !ok || now.Sub(ent.last) > t.window() {
		return 0
	}
	return t.delayForLocked(ent.failures + 1)
}

// delayForLocked is the wait for the nth consecutive attempt from one address.
func (t *Throttle) delayForLocked(attempts int) time.Duration {
	if attempts <= 1 {
		return 0
	}
	d := t.base()
	for i := 2; i < attempts; i++ {
		d *= 2
		if d >= t.max() {
			return t.max()
		}
	}
	if d > t.max() {
		return t.max()
	}
	return d
}

// Reset forgets addr, which is what a correct password does.
func (t *Throttle) Reset(addr string) {
	host := NormalizeAddr(addr)
	if host == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, host)
}

// sweepLocked drops addresses that have been quiet for longer than the window,
// and, if the table is still too large, everything in it: the alternative is a
// map that a spray can grow without bound.
func (t *Throttle) sweepLocked(now time.Time) {
	for host, ent := range t.entries {
		if now.Sub(ent.last) > t.window() {
			delete(t.entries, host)
		}
	}
	if len(t.entries) >= maxThrottleEntries {
		t.entries = make(map[string]throttleEntry)
	}
}

// ClientAddr is the address a throttle should count an attempt against.
//
// It exists because the deployment the documentation recommends - a
// TLS-terminating proxy in front of a loopback listener - hands every request in
// the world the same RemoteAddr, 127.0.0.1, which the loopback exemption would
// then wave through. So when, and only when, the connection itself came from
// loopback, the forwarded client address is believed: on that path the header
// can only be set by something already on this machine. A connection from
// anywhere else is counted by where it actually came from, because there the
// header is the attacker's to write.
func ClientAddr(remoteAddr, forwardedFor, realIP string) string {
	direct := NormalizeAddr(remoteAddr)
	if !IsLoopbackAddr(direct) {
		return direct
	}
	// The leftmost entry of X-Forwarded-For is the client the first proxy saw.
	if v := strings.TrimSpace(strings.Split(forwardedFor, ",")[0]); v != "" {
		if host := NormalizeAddr(v); host != "" {
			return host
		}
	}
	if host := NormalizeAddr(realIP); host != "" {
		return host
	}
	return direct
}

// NormalizeAddr reduces a RemoteAddr ("host:port", a bare host, or an IPv6
// address in brackets) to the host part, so the same client is one row whichever
// ephemeral port it dialled from.
func NormalizeAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	addr = strings.Trim(addr, "[]")
	if ap, err := netip.ParseAddr(addr); err == nil {
		return ap.Unmap().String()
	}
	return addr
}

// IsLoopbackAddr reports whether a normalized host is this machine talking to
// itself.
func IsLoopbackAddr(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "":
		return true
	}
	if ap, err := netip.ParseAddr(host); err == nil {
		return ap.Unmap().IsLoopback()
	}
	return false
}
