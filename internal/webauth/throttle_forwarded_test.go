package webauth

import (
	"testing"
	"time"
)

func TestClientAddrForwardedSpoofingDoesNotEvadeThrottle(t *testing.T) {
	throttle := &Throttle{Base: time.Second}
	for i, header := range []string{
		"127.0.0.1, 203.0.113.7",
		"198.51.100.1, 203.0.113.7",
		"198.51.100.2, 203.0.113.7",
	} {
		addr := ClientAddr("127.0.0.1:1234", header, "")
		if addr != "203.0.113.7" {
			t.Errorf("header %q resolved to %q", header, addr)
		}
		if d := throttle.Penalize(addr); i > 0 && d == 0 {
			t.Errorf("attempt %d escaped throttle", i+1)
		}
	}
}

func TestClientAddrForwardedUsesRightmostValidIP(t *testing.T) {
	if got := ClientAddr("127.0.0.1:1234", "127.0.0.1, 203.0.113.7, invalid", ""); got != "203.0.113.7" {
		t.Fatalf("got %q", got)
	}
}
