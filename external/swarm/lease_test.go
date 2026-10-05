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
