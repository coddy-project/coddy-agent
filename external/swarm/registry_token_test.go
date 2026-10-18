//go:build swarm

package swarm

import (
	"testing"
	"time"
)

func storedToken(t *testing.T, r *Registry, name string) string {
	t.Helper()
	n, ok := r.Node(name)
	if !ok {
		t.Fatalf("node %q is not registered", name)
	}
	return n.Token
}

// The owner's renewal replaces the stored token with the one it carries, an
// empty one included: a node that stopped sending a token is cut off.
func TestOwnerRenewalReplacesTheStoredToken(t *testing.T) {
	cases := []struct {
		name, first, renewal, want string
	}{
		{"same token", "tok-a", "tok-a", "tok-a"},
		{"a different token", "tok-a", "tok-b", "tok-b"},
		{"an empty token erases", "tok-a", "", ""},
		{"from empty to set", "", "tok-b", "tok-b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newTestRegistry(t, time.Minute)
			req := directRequest("nas02")
			req.Token = tc.first
			res, err := r.Register(req)
			if err != nil {
				t.Fatal(err)
			}
			if got := storedToken(t, r, "nas02"); got != tc.first {
				t.Fatalf("first claim stored %q, want %q", got, tc.first)
			}
			req.Token = tc.renewal
			req.LeaseSecret = res.LeaseSecret
			if _, err := r.Register(req); err != nil {
				t.Fatal(err)
			}
			if got := storedToken(t, r, "nas02"); got != tc.want {
				t.Fatalf("after renewal the token is %q, want %q", got, tc.want)
			}
		})
	}
}

// A registration that does not own the name changes nothing, whatever token it
// carries.
func TestNonOwnerRegistrationLeavesTheTokenAlone(t *testing.T) {
	r, _ := newTestRegistry(t, time.Minute)
	req := directRequest("nas02")
	if _, err := r.Register(req); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"", "not-the-secret"} {
		imp := directRequest("nas02")
		imp.Token = ""
		imp.LeaseSecret = secret
		if _, err := r.Register(imp); err != ErrNameTaken {
			t.Fatalf("secret %q: err = %v, want ErrNameTaken", secret, err)
		}
	}
	if got := storedToken(t, r, "nas02"); got != "node-token-nas02" {
		t.Fatalf("token = %q, want it untouched", got)
	}
}

// A pinned upstream is registered once and never renewed, so its token stays.
func TestPinnedNodeKeepsItsToken(t *testing.T) {
	r, clock := newTestRegistry(t, time.Minute)
	if _, err := r.Register(directRequest("up1")); err != nil {
		t.Fatal(err)
	}
	r.Pin("up1")
	clock.Advance(10 * time.Minute)
	if got := storedToken(t, r, "up1"); got != "node-token-up1" {
		t.Fatalf("pinned token = %q", got)
	}
}
