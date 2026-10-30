//go:build swarm

package swarm

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A name stays its owner's after the lease is gone: the relay restarted, or
// the node was offline past the grace period. Someone else with a pairing
// token cannot take it, the owner takes it back with its secret, and
// DELETE /swarm/nodes/{node} still frees it.
func TestANameStaysBoundToItsSecretAcrossARestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "swarm-names.json")
	first, _ := newTestRegistry(t, time.Minute)
	first.SetBindingsFile(file)
	res, err := first.Register(directRequest("nas02"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), res.LeaseSecret) {
		t.Fatalf("the file keeps the lease secret itself: %s", data)
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(file); err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("file mode = %v, %v; want 0600", st.Mode().Perm(), err)
		}
	}

	// The relay restarts: a new registry over the same file, no leases.
	second, _ := newTestRegistry(t, time.Minute)
	second.SetBindingsFile(file)
	if _, err := second.Register(directRequest("nas02")); !errors.Is(err, ErrNameBound) {
		t.Fatalf("a registration without the secret took a bound name: %v", err)
	}
	wrong := directRequest("nas02")
	wrong.LeaseSecret = "not-the-secret"
	if _, err := second.Register(wrong); !errors.Is(err, ErrNameBound) {
		t.Fatalf("a wrong secret took a bound name: %v", err)
	}
	owner := directRequest("nas02")
	owner.LeaseSecret = res.LeaseSecret
	back, err := second.Register(owner)
	if err != nil {
		t.Fatalf("the owner could not take its name back: %v", err)
	}
	if back.LeaseSecret != res.LeaseSecret {
		t.Fatal("the owner's secret changed on its return")
	}

	if !second.Delete("nas02") {
		t.Fatal("the administrative delete found no lease")
	}
	third, _ := newTestRegistry(t, time.Minute)
	third.SetBindingsFile(file)
	if _, err := third.Register(directRequest("nas02")); err != nil {
		t.Fatalf("a deleted name is still bound: %v", err)
	}
}

// A lease reaped after the grace period leaves the binding behind.
func TestAReapedLeaseKeepsItsName(t *testing.T) {
	r, clock := newTestRegistry(t, time.Minute)
	r.SetBindingsFile(filepath.Join(t.TempDir(), "swarm-names.json"))
	res, err := r.Register(directRequest("nas02"))
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(5 * time.Minute)
	if r.Len() != 0 {
		t.Fatal("the lease was not reaped")
	}
	if _, err := r.Register(directRequest("nas02")); !errors.Is(err, ErrNameBound) {
		t.Fatalf("a reaped name went to the first comer: %v", err)
	}
	owner := directRequest("nas02")
	owner.LeaseSecret = res.LeaseSecret
	if _, err := r.Register(owner); err != nil {
		t.Fatalf("the owner could not take its name back: %v", err)
	}
}

// After a restart a name has a binding and no lease, and the administrative
// delete still frees it: that is the way back for a node that lost its secret.
func TestDeleteFreesABoundNameThatHasNoLease(t *testing.T) {
	file := filepath.Join(t.TempDir(), "swarm-names.json")
	first, _ := newTestRegistry(t, time.Minute)
	first.SetBindingsFile(file)
	if _, err := first.Register(directRequest("nas02")); err != nil {
		t.Fatal(err)
	}

	second, _ := newTestRegistry(t, time.Minute)
	second.SetBindingsFile(file)
	if !second.Delete("nas02") {
		t.Fatal("the administrative delete did not find a name bound on disk")
	}
	if second.Delete("nas02") {
		t.Fatal("a second delete still found the name")
	}
	if _, err := second.Register(directRequest("nas02")); err != nil {
		t.Fatalf("a deleted name is still bound: %v", err)
	}
}
