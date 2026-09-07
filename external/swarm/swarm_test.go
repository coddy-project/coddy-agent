//go:build swarm

package swarm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// testClock lets a lease expire without the test sleeping through its TTL.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestRegistry(t *testing.T, ttl time.Duration) (*Registry, *testClock) {
	t.Helper()
	clock := &testClock{now: time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)}
	r := NewRegistry(ttl)
	r.now = clock.Now
	return r, clock
}

func directRequest(name string) swarmdto.RegisterRequest {
	return swarmdto.RegisterRequest{
		Name:         name,
		Kind:         swarmdto.KindAgent,
		Transport:    swarmdto.TransportDirect,
		AdvertiseURL: "http://" + name + ":12345",
		InstanceUUID: "uuid-" + name,
		Token:        "node-token-" + name,
	}
}

func TestRegisterMintsALeaseSecret(t *testing.T) {
	r, _ := newTestRegistry(t, time.Minute)
	res, err := r.Register(directRequest("nas02"))
	if err != nil {
		t.Fatal(err)
	}
	if res.NodeID != "nas02" {
		t.Fatalf("node id = %q, want nas02", res.NodeID)
	}
	if len(res.LeaseSecret) < 32 {
		t.Fatalf("lease secret looks too short to be a secret: %q", res.LeaseSecret)
	}
	if res.Generation != 1 {
		t.Fatalf("generation = %d, want 1", res.Generation)
	}
	if res.TTLSeconds != 60 {
		t.Fatalf("ttl = %d, want 60", res.TTLSeconds)
	}
}

// The whole point of a per-lease secret: a shared pairing credential authorises
// joining the swarm, never taking over a peer's name.
func TestRegisterRefusesToStealALiveName(t *testing.T) {
	r, _ := newTestRegistry(t, time.Minute)
	first, err := r.Register(directRequest("nas02"))
	if err != nil {
		t.Fatal(err)
	}

	impostor := directRequest("nas02")
	impostor.AdvertiseURL = "http://attacker.example"
	impostor.Token = "attacker-token"
	if _, err := r.Register(impostor); err != ErrNameTaken {
		t.Fatalf("registration without the secret = %v, want ErrNameTaken", err)
	}
	impostor.LeaseSecret = "not-the-secret"
	if _, err := r.Register(impostor); err != ErrNameTaken {
		t.Fatalf("registration with a wrong secret = %v, want ErrNameTaken", err)
	}

	node, ok := r.Node("nas02")
	if !ok {
		t.Fatal("node vanished")
	}
	if node.Info.URL != "http://nas02:12345" {
		t.Fatalf("a refused registration still moved the route: %q", node.Info.URL)
	}
	if node.Token != "node-token-nas02" {
		t.Fatalf("a refused registration still replaced the credential: %q", node.Token)
	}

	// The owner, holding the secret, renews rather than being refused.
	renew := directRequest("nas02")
	renew.LeaseSecret = first.LeaseSecret
	second, err := r.Register(renew)
	if err != nil {
		t.Fatalf("owner renewal: %v", err)
	}
	if second.Generation != 2 {
		t.Fatalf("generation = %d, want it to move on renewal", second.Generation)
	}
	if second.LeaseSecret != first.LeaseSecret {
		t.Fatal("a renewal should keep the same secret")
	}
}

func TestLeaseGoesOfflineThenExpires(t *testing.T) {
	r, clock := newTestRegistry(t, time.Minute)
	if _, err := r.Register(directRequest("nas02")); err != nil {
		t.Fatal(err)
	}
	if node, _ := r.Node("nas02"); !node.Info.Online {
		t.Fatal("a fresh registration should be online")
	}

	// Past the TTL the node is stale but still worth showing: an operator wants
	// to see that it exists and went quiet, not watch it disappear.
	clock.Advance(90 * time.Second)
	node, ok := r.Node("nas02")
	if !ok {
		t.Fatal("the lease was dropped the moment it went stale")
	}
	if node.Info.Online {
		t.Fatal("a stale lease should not read as online")
	}
	if len(r.Online()) != 0 {
		t.Fatal("a stale node should not be fanned out to")
	}

	// Past the grace period it is gone and the name is free again.
	clock.Advance(2 * time.Minute)
	if _, ok := r.Node("nas02"); ok {
		t.Fatal("the lease outlived its grace period")
	}
	if _, err := r.Register(directRequest("nas02")); err != nil {
		t.Fatalf("the freed name should be claimable: %v", err)
	}
}

func TestDeleteIsTheTakeoverPath(t *testing.T) {
	r, _ := newTestRegistry(t, time.Minute)
	if _, err := r.Register(directRequest("nas02")); err != nil {
		t.Fatal(err)
	}
	if !r.Delete("nas02") {
		t.Fatal("Delete reported no such node")
	}
	if r.Delete("nas02") {
		t.Fatal("Delete reported success twice")
	}
	if _, err := r.Register(directRequest("nas02")); err != nil {
		t.Fatalf("after a delete the name should be free: %v", err)
	}
}

// The public row is what a client sees. It must not carry a credential, and the
// type is shaped so a future edit cannot add one by accident.
func TestListNeverSerialisesACredential(t *testing.T) {
	r, _ := newTestRegistry(t, time.Minute)
	req := directRequest("nas02")
	req.Token = "s3cr3t-canary-token"
	res, err := r.Register(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(r.List())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "s3cr3t-canary-token") {
		t.Fatalf("the node credential reached the public list: %s", raw)
	}
	if strings.Contains(string(raw), res.LeaseSecret) {
		t.Fatalf("the lease secret reached the public list: %s", raw)
	}
}

func TestListOrdersOnlineFirstThenByName(t *testing.T) {
	r, clock := newTestRegistry(t, time.Minute)
	for _, name := range []string{"zulu", "alpha", "mike"} {
		if _, err := r.Register(directRequest(name)); err != nil {
			t.Fatal(err)
		}
	}
	// Let alpha go stale while the others stay fresh.
	clock.Advance(90 * time.Second)
	for _, name := range []string{"zulu", "mike"} {
		req := directRequest(name)
		node, _ := r.Node(name)
		_ = node
		req.LeaseSecret = secretOf(t, r, name)
		if _, err := r.Register(req); err != nil {
			t.Fatal(err)
		}
	}
	got := []string{}
	for _, n := range r.List() {
		got = append(got, fmt.Sprintf("%s:%v", n.Name, n.Online))
	}
	want := []string{"mike:true", "zulu:true", "alpha:false"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("List() = %v, want %v", got, want)
	}
}

// secretOf reaches into the registry the way only a test may, so a renewal can
// be exercised without threading secrets through every helper.
func secretOf(t *testing.T, r *Registry, name string) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.nodes[name]
	if !ok {
		t.Fatalf("no lease for %q", name)
	}
	return l.secret
}

// fakeTunnel stands in for a dialled-out connection.
type fakeTunnel struct {
	mu     sync.Mutex
	alive  bool
	closed int
}

func (f *fakeTunnel) RoundTripper() http.RoundTripper { return nil }
func (f *fakeTunnel) TargetURL() *url.URL             { u, _ := url.Parse("http://node.swarm.invalid"); return u }

func (f *fakeTunnel) Alive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive
}

func (f *fakeTunnel) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alive = false
	f.closed++
	return nil
}

func tunnelRequest(name string) swarmdto.RegisterRequest {
	return swarmdto.RegisterRequest{
		Name:         name,
		Kind:         swarmdto.KindAgent,
		Transport:    swarmdto.TransportTunnel,
		InstanceUUID: "uuid-" + name,
		Token:        "node-token-" + name,
	}
}

func TestAttachTransportNeedsTheLeaseSecret(t *testing.T) {
	r, _ := newTestRegistry(t, time.Minute)
	res, err := r.Register(tunnelRequest("inner"))
	if err != nil {
		t.Fatal(err)
	}
	tun := &fakeTunnel{alive: true}
	if err := r.AttachTransport("inner", "wrong", tun); err != ErrLeaseSecret {
		t.Fatalf("attach with a wrong secret = %v, want ErrLeaseSecret", err)
	}
	if err := r.AttachTransport("nobody", res.LeaseSecret, tun); err != ErrUnknownNode {
		t.Fatalf("attach to an unknown node = %v, want ErrUnknownNode", err)
	}
	if err := r.AttachTransport("inner", res.LeaseSecret, tun); err != nil {
		t.Fatalf("attach by the owner: %v", err)
	}
	node, ok := r.Node("inner")
	if !ok || !node.Info.Online {
		t.Fatal("a node holding a live tunnel should be online")
	}
	if node.Info.Transport != swarmdto.TransportTunnel {
		t.Fatalf("transport = %q, want tunnel", node.Info.Transport)
	}
}

// A reconnect replaces the connection. The old one is closed rather than left
// half-open, so work in flight on it fails immediately instead of hanging.
func TestAttachTransportReplacesTheOldConnection(t *testing.T) {
	r, _ := newTestRegistry(t, time.Minute)
	res, err := r.Register(tunnelRequest("inner"))
	if err != nil {
		t.Fatal(err)
	}
	first := &fakeTunnel{alive: true}
	if err := r.AttachTransport("inner", res.LeaseSecret, first); err != nil {
		t.Fatal(err)
	}
	before, _ := r.Node("inner")

	second := &fakeTunnel{alive: true}
	if err := r.AttachTransport("inner", res.LeaseSecret, second); err != nil {
		t.Fatal(err)
	}
	if first.closed == 0 {
		t.Fatal("the replaced connection was left open")
	}
	after, _ := r.Node("inner")
	if after.Info.Generation <= before.Info.Generation {
		t.Fatalf("generation did not move on reconnect: %d -> %d", before.Info.Generation, after.Info.Generation)
	}
	if after.Transport != second {
		t.Fatal("the registry kept routing to the replaced connection")
	}
}

// For a tunnel node the connection is the liveness signal, so losing it must
// take the node offline without waiting for a heartbeat clock.
func TestATunnelNodeGoesOfflineWhenItsConnectionDies(t *testing.T) {
	r, _ := newTestRegistry(t, time.Minute)
	res, err := r.Register(tunnelRequest("inner"))
	if err != nil {
		t.Fatal(err)
	}
	tun := &fakeTunnel{alive: true}
	if err := r.AttachTransport("inner", res.LeaseSecret, tun); err != nil {
		t.Fatal(err)
	}
	if len(r.Online()) != 1 {
		t.Fatal("expected the tunnel node to be online")
	}
	_ = tun.Close()
	if len(r.Online()) != 0 {
		t.Fatal("a node whose connection died should not be fanned out to")
	}
	if _, ok := r.Node("inner"); !ok {
		t.Fatal("the lease should linger so the node reads as offline, not missing")
	}
}

func TestRegistryIsSafeUnderConcurrentUse(t *testing.T) {
	r, _ := newTestRegistry(t, time.Minute)
	secrets := map[string]string{}
	for _, name := range []string{"a", "b", "c", "d"} {
		res, err := r.Register(directRequest(name))
		if err != nil {
			t.Fatal(err)
		}
		secrets[name] = res.LeaseSecret
	}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := []string{"a", "b", "c", "d"}[i%4]
			switch i % 4 {
			case 0:
				req := directRequest(name)
				req.LeaseSecret = secrets[name]
				_, _ = r.Register(req)
			case 1:
				_, _ = r.Node(name)
			case 2:
				_ = r.List()
			case 3:
				_ = r.Online()
			}
		}(i)
	}
	wg.Wait()
	if r.Len() != 4 {
		t.Fatalf("registry holds %d nodes, want 4", r.Len())
	}
}
