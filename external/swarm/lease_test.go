//go:build swarm

package swarm

import (
	"testing"
	"time"
)

// Reaping is lazy: an entry past its grace period is dropped by the next
// registry call. A registration is such a call and must reap before it decides,
// so a node that lost its secret can take its own name back once the grace
// period is over, without anybody listing the registry first. The test never
// calls List, Node or Len between the clock moving and the claim, because each of
// them reaps and would hide the omission.
func TestARegistrationReapsALeasePastItsGraceWithoutHelpFromAnyOtherCall(t *testing.T) {
	r, clock := newTestRegistry(t, time.Minute)
	first, err := r.Register(directRequest("nas02"))
	if err != nil {
		t.Fatal(err)
	}

	// Inside the grace period the name is still held. Refusing a stranger here
	// is what keeps a pairing token from taking the name of a node that is only
	// late with its heartbeat.
	clock.Advance(90 * time.Second)
	if _, err := r.Register(directRequest("nas02")); err != ErrNameTaken {
		t.Fatalf("a claim inside the grace period = %v, want ErrNameTaken", err)
	}

	clock.Advance(2 * time.Minute)
	second, err := r.Register(directRequest("nas02"))
	if err != nil {
		t.Fatalf("a claim after the grace period was refused: %v", err)
	}
	if second.LeaseSecret == first.LeaseSecret {
		t.Fatal("a new lease must come with a new secret")
	}

	// The former owner's secret is worthless once the name has a new owner.
	stale := directRequest("nas02")
	stale.LeaseSecret = first.LeaseSecret
	if _, err := r.Register(stale); err != ErrNameTaken {
		t.Fatalf("a renewal with the old secret = %v, want ErrNameTaken", err)
	}
}

// A tunnel lease follows its connection rather than a clock. When the
// connection dies and nothing detaches it, the next registry call has to put
// the lease on the clock itself: offline at once, still listed through the
// grace period so an operator sees the node went away, then gone so the name
// can be claimed again. Without that, a node whose connection died quietly
// would hold its name for good and only an operator could take it back.
func TestATunnelLeaseWhoseConnectionDiedQuietlyIsReapedAfterItsGrace(t *testing.T) {
	r, clock := newTestRegistry(t, time.Minute)
	res, err := r.Register(tunnelRequest("inner"))
	if err != nil {
		t.Fatal(err)
	}
	tun := &fakeTunnel{alive: true}
	if err := r.AttachTransport("inner", res.LeaseSecret, tun); err != nil {
		t.Fatal(err)
	}

	_ = tun.Close() // the connection is gone and DetachTransport is never called
	if node, ok := r.Node("inner"); !ok || node.Info.Online {
		t.Fatalf("a lease whose connection died should read as offline, not missing: listed=%v online=%v", ok, node.Info.Online)
	}

	clock.Advance(30 * time.Second)
	if _, err := r.Register(tunnelRequest("inner")); err != ErrNameTaken {
		t.Fatalf("a claim inside the grace period = %v, want ErrNameTaken", err)
	}

	clock.Advance(time.Minute)
	if _, err := r.Register(tunnelRequest("inner")); err != nil {
		t.Fatalf("a claim after the grace period was refused: %v", err)
	}
}

// A tunnel lease is seen for as long as its connection holds, so when the
// connection goes away the node was last seen then, not when it registered:
// a node behind a relay that answers the System page "offline, last seen 11
// minutes ago" for a tunnel that dropped a second ago sends the operator
// looking for the wrong outage.
func TestATunnelNodeIsLastSeenWhenItsConnectionWentAway(t *testing.T) {
	for _, quietly := range []bool{false, true} {
		r, clock := newTestRegistry(t, time.Minute)
		res, err := r.Register(tunnelRequest("inner"))
		if err != nil {
			t.Fatal(err)
		}
		tun := &fakeTunnel{alive: true}
		if err := r.AttachTransport("inner", res.LeaseSecret, tun); err != nil {
			t.Fatal(err)
		}

		clock.Advance(11 * time.Minute)
		_ = tun.Close()
		if !quietly {
			r.DetachTransport("inner", tun)
		}
		node, ok := r.Node("inner")
		if !ok {
			t.Fatalf("quietly=%v: the lease went missing inside its grace period", quietly)
		}
		want := clock.Now().UTC().Format(time.RFC3339)
		if node.Info.LastSeen != want {
			t.Fatalf("quietly=%v: last seen %s, want %s, when the connection went away", quietly, node.Info.LastSeen, want)
		}
	}
}
