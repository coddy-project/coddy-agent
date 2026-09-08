//go:build swarm

package swarm

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/netx"
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
	// These tests advertise loopback and private addresses on purpose; the
	// policy that refuses them by default has its own tests in internal/netx.
	r.SetEgressPolicy(netx.EgressPolicy{AllowLoopback: true, AllowPrivate: true})
	return r, clock
}

func directRequest(name string) swarmdto.RegisterRequest {
	return swarmdto.RegisterRequest{
		Name:         name,
		Kind:         swarmdto.KindAgent,
		Transport:    swarmdto.TransportDirect,
		AdvertiseURL: "http://127.0.0.1:12345",
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
	impostor.AdvertiseURL = "http://127.0.0.2:12345"
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
	if node.Info.URL != "http://127.0.0.1:12345" {
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
	secrets := registerAll(t, r, "zulu", "alpha", "mike")
	// Let alpha go stale while the others stay fresh.
	clock.Advance(90 * time.Second)
	for _, name := range []string{"zulu", "mike"} {
		req := directRequest(name)
		req.LeaseSecret = secrets[name]
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

// registerAll claims each name and keeps the secret the relay handed back, so a
// renewal can be exercised through the same surface a node uses rather than by
// reading the registry's own map.
func registerAll(t *testing.T, r *Registry, names ...string) map[string]string {
	t.Helper()
	secrets := map[string]string{}
	for _, name := range names {
		res, err := r.Register(directRequest(name))
		if err != nil {
			t.Fatalf("register %q: %v", name, err)
		}
		secrets[name] = res.LeaseSecret
	}
	return secrets
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

// ---- mount edges ----

func TestMountAllowsOnlyTheDataPlane(t *testing.T) {
	carried := []string{
		"/v1/models",
		"/v1/responses",
		"/coddy/sessions",
		"/coddy/sessions/abc/messages",
		"/coddy/hooks",       // a route that did not exist when the relay was written
		"/coddy/subagents/x", // likewise
		"/swarm/info",
		"/swarm/nodes",
		"/swarm/sessions",
		"/swarm/topology",
		"/swarm/nodes/inner/coddy/sessions", // a further hop
	}
	for _, path := range carried {
		if !mountAllows(http.MethodGet, path) {
			t.Errorf("mount should carry %q", path)
		}
	}
	refused := []string{
		"/swarm/register",
		"/swarm/tunnel",
		"/swarm/register/anything",
		"/",
		"/docs",
		"/openapi.yaml",
		"/metrics",
	}
	for _, path := range refused {
		if mountAllows(http.MethodGet, path) {
			t.Errorf("mount should refuse %q", path)
		}
	}

	// Reading a child relay's node list is ordinary; evicting from it is the
	// child's own control plane, and the two differ only by method.
	if !mountAllows(http.MethodGet, "/swarm/nodes/victim") {
		t.Error("reading a node through a chain should be allowed")
	}
	if mountAllows(http.MethodDelete, "/swarm/nodes/victim") {
		t.Error("a mount must not carry a node eviction to a child relay")
	}
	// A delete deeper down belongs to the node's own API, not the relay's.
	if !mountAllows(http.MethodDelete, "/swarm/nodes/child/coddy/sessions/abc") {
		t.Error("deleting a session on a node is the node's API, not the relay's")
	}
	if !mountAllows(http.MethodDelete, "/coddy/sessions/abc") {
		t.Error("deleting a session should be carried")
	}
}

func TestMountRemainderRejectsAmbiguousPaths(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
		ok   bool
	}{
		{"plain", "/swarm/nodes/nas02/coddy/sessions", "/coddy/sessions", true},
		{"root", "/swarm/nodes/nas02", "/", true},
		{"trailing slash", "/swarm/nodes/nas02/", "/", true},
		{"encoded space survives", "/swarm/nodes/nas02/coddy/x%20y", "/coddy/x%20y", true},
		{"encoded separator", "/swarm/nodes/nas02/coddy/a%2Fb", "", false},
		{"encoded backslash", "/swarm/nodes/nas02/coddy/a%5Cb", "", false},
		{"dot segment", "/swarm/nodes/nas02/coddy/../swarm/register", "", false},
		{"single dot", "/swarm/nodes/nas02/./coddy", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			got, err := mountRemainder(req, "nas02")
			if !tc.ok {
				if err == nil {
					t.Fatalf("mountRemainder(%q) = %q, want an error", tc.path, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("mountRemainder(%q): %v", tc.path, err)
			}
			if got != tc.want {
				t.Fatalf("mountRemainder(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// Whatever a client sends, the node must see the relay's view of who is
// calling, not the client's claims about itself.
func TestMountScrubsClientSuppliedHeaders(t *testing.T) {
	var seen http.Header
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		seen.Set("X-Seen-Query", r.URL.RawQuery)
		w.WriteHeader(http.StatusOK)
	}))
	defer node.Close()

	relay, ts := mountTestRelay(t, node.URL)
	defer ts.Close()
	_ = relay

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/sessions?access_token=client-secret&keep=yes", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer client-secret")
	req.Header.Set("Cookie", "session=mine")
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	req.Header.Set("X-Real-IP", "10.0.0.1")
	req.Header.Set("Forwarded", "for=10.0.0.1")
	req.Header.Set("X-Coddy-Swarm-Path", "forged-uuid")
	req.Header.Set("X-Coddy-Session-ID", "sess_keep")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()

	if got := seen.Get("Authorization"); got != "Bearer node-secret" {
		t.Fatalf("node saw authorization %q, want the node's own credential", got)
	}
	for _, h := range []string{"Cookie", "X-Forwarded-For", "X-Real-Ip", "Forwarded", "X-Coddy-Swarm-Path"} {
		if v := seen.Get(h); v != "" {
			t.Errorf("header %s reached the node as %q", h, v)
		}
	}
	if got := seen.Get("X-Coddy-Session-ID"); got != "sess_keep" {
		t.Errorf("a legitimate header was dropped: X-Coddy-Session-ID = %q", got)
	}
	query := seen.Get("X-Seen-Query")
	if strings.Contains(query, "access_token") {
		t.Errorf("the relay's own credential reached the node's query string: %q", query)
	}
	if !strings.Contains(query, "keep=yes") {
		t.Errorf("an ordinary query parameter was lost: %q", query)
	}
}

// A node that is registered but unreachable has to be reported as that, naming
// the hop, rather than as an unattributed gateway failure.
func TestMountReportsAnUnreachableNodeByName(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := dead.URL
	dead.Close() // nothing listens there any more

	_, ts := mountTestRelay(t, url)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/sessions", nil)
	req.Header.Set("Authorization", "Bearer client-secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d, want 502, body %s", res.StatusCode, body)
	}
	var wrap struct {
		Error struct {
			Node   string `json:"node"`
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if wrap.Error.Node != "nas02" {
		t.Fatalf("the failure does not name the hop: %s", body)
	}
	if wrap.Error.Reason == "" {
		t.Fatalf("the failure carries no reason: %s", body)
	}
}

// mountTestRelay builds a relay holding one direct node at nodeURL.
func mountTestRelay(t *testing.T, nodeURL string) (*Server, *httptest.Server) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1" // a loopback relay may reach loopback nodes
	cfg.Swarm.AuthToken = "client-secret"
	cfg.Swarm.PairingTokens = []string{"pair-secret"}
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.registry.Register(swarmdto.RegisterRequest{
		Name:         "nas02",
		Kind:         swarmdto.KindAgent,
		Transport:    swarmdto.TransportDirect,
		AdvertiseURL: nodeURL,
		InstanceUUID: "uuid-nas02",
		Token:        "node-secret",
	}); err != nil {
		t.Fatal(err)
	}
	return srv, httptest.NewServer(srv.Handler())
}

// A hand-written path can walk a ring indefinitely; the fan-out's hop budget
// lives in a header the client never sees, so the path needs its own bound.
func TestMountRefusesAPathThatWalksTooManyRelays(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer node.Close()
	_, ts := mountTestRelay(t, node.URL)
	defer ts.Close()

	deep := ts.URL + "/swarm/nodes/nas02"
	for i := 0; i < swarmMaxHops; i++ {
		deep += "/swarm/nodes/next"
	}
	deep += "/coddy/sessions"

	req, _ := http.NewRequest(http.MethodGet, deep, nil)
	req.Header.Set("Authorization", "Bearer client-secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusLoopDetected {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d, want 508 for an over-long chain: %s", res.StatusCode, body)
	}
}

// An EventSource cannot set a header, so a browser reattaching a stream through
// a relay has only the query. The relay accepts it there and nowhere else, and
// never passes it on.
func TestMountAcceptsAQueryTokenOnlyWhereAHeaderIsImpossible(t *testing.T) {
	var seenQuery string
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer node.Close()
	_, ts := mountTestRelay(t, node.URL)
	defer ts.Close()

	stream := ts.URL + "/swarm/nodes/nas02/coddy/sessions/abc/composer-stream?access_token=client-secret"
	res, err := http.Get(stream) //nolint:noctx // short-lived test request
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("a stream reattach with a query token got %d, want it accepted", res.StatusCode)
	}
	if strings.Contains(seenQuery, "access_token") {
		t.Fatalf("the relay's own credential reached the node: %q", seenQuery)
	}

	// The same token on an ordinary route is not a credential.
	plain, err := http.Get(ts.URL + "/swarm/nodes/nas02/coddy/sessions?access_token=client-secret") //nolint:noctx // short-lived test request
	if err != nil {
		t.Fatal(err)
	}
	_ = plain.Body.Close()
	if plain.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a query token on a normal route got %d, want 401", plain.StatusCode)
	}
}

// A row crossing a relay carries fields this relay does not model itself:
// activity, permission state, a subagent link. They are flattened on the way
// out, so they have to be gathered on the way back in or a chain quietly
// strips them.
func TestSessionRowSurvivesARelayHopWithItsExtras(t *testing.T) {
	original := []byte(`{
		"id":"sess_a","title":"work","updatedAt":"2026-09-08T12:00:00Z","cwd":"/srv",
		"agent_uuid":"agent-1","node_path":["inner","agent7"],"node_name":"agent7",
		"turnActive":true,"permissionPending":false,"subagent":{"parent":"sess_p"}
	}`)
	var row sessionRow
	if err := json.Unmarshal(original, &row); err != nil {
		t.Fatal(err)
	}
	if row.ID != "sess_a" || row.NodeName != "agent7" || row.AgentUUID != "agent-1" {
		t.Fatalf("known fields did not survive: %+v", row)
	}
	if len(row.Extra) == 0 {
		t.Fatal("the fields this relay does not model were dropped on the way in")
	}

	// A parent prefixes its own hop and re-emits the row.
	row.NodePath = append([]string{"outer"}, row.NodePath...)
	out, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var seen map[string]interface{}
	if err := json.Unmarshal(out, &seen); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"turnActive", "permissionPending", "subagent"} {
		if _, ok := seen[key]; !ok {
			t.Errorf("field %q was lost crossing a relay: %s", key, out)
		}
	}
	if got, _ := seen["node_path"].([]interface{}); len(got) != 3 {
		t.Fatalf("the hop was not prefixed: %s", out)
	}
}

// A node the relay still holds a lease for but cannot reach has to say so.
// Dropping it silently makes "this machine is down" look identical to "this
// machine has no work", which is the more alarming of the two.
func TestAggregationWarnsAboutANodeWhoseLeaseWentStale(t *testing.T) {
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.AuthToken = "client-secret"
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)}
	srv.registry.now = clock.Now
	srv.registry.SetEgressPolicy(netx.EgressPolicy{AllowLoopback: true})
	if _, err := srv.registry.Register(swarmdto.RegisterRequest{
		Name: "quiet", Kind: swarmdto.KindAgent, Transport: swarmdto.TransportDirect,
		AdvertiseURL: "http://127.0.0.1:12345", InstanceUUID: "u", Token: "t",
	}); err != nil {
		t.Fatal(err)
	}

	// Past its lease but inside the grace period: still known, not reachable.
	clock.Advance(100 * time.Second)
	if len(srv.registry.Online()) != 0 {
		t.Fatal("the lease should be stale by now")
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/swarm/sessions", nil)
	req.Header.Set("Authorization", "Bearer client-secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)

	var out struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	var named bool
	for _, w := range out.Warnings {
		if strings.Contains(w, "quiet") {
			named = true
		}
	}
	if !named {
		t.Fatalf("a stale node vanished without a word: %s", body)
	}
}

// Judging only the escaped form is not enough: %2e%2e survives it and becomes
// ".." the moment anything decodes the path, which is how a request aimed at a
// node's API climbs back out into the relay's own routes.
func TestMountRefusesEncodedRelativeSegments(t *testing.T) {
	traversals := []string{
		"/swarm/nodes/nas02/coddy/%2e%2e/swarm/register",
		"/swarm/nodes/nas02/coddy/%2E%2E/swarm/register",
		"/swarm/nodes/nas02/%2e/coddy/sessions",
		"/swarm/nodes/nas02/coddy/%2e%2e%2f%2e%2e/etc",
		"/swarm/nodes/nas02/coddy/../swarm/register",
	}
	for _, raw := range traversals {
		t.Run(raw, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, raw, nil)
			if rest, err := mountRemainder(req, "nas02"); err == nil {
				t.Fatalf("mountRemainder accepted %q as %q", raw, rest)
			}
		})
	}

	// An ordinary escape is still carried through untouched.
	req := httptest.NewRequest(http.MethodGet, "/swarm/nodes/nas02/coddy/x%20y", nil)
	rest, err := mountRemainder(req, "nas02")
	if err != nil {
		t.Fatalf("a legitimate escape was refused: %v", err)
	}
	if rest != "/coddy/x%20y" {
		t.Fatalf("rest = %q", rest)
	}
}

// A heartbeat that cannot build its new route must leave the node exactly as it
// was. Tearing the working one down first means the owner knocks itself off the
// air until some later heartbeat happens to succeed.
func TestRenewalKeepsTheWorkingRouteWhenTheNewOneCannotBeBuilt(t *testing.T) {
	r, _ := newTestRegistry(t, time.Minute)
	res, err := r.Register(directRequest("nas02"))
	if err != nil {
		t.Fatal(err)
	}
	before, ok := r.Node("nas02")
	if !ok || before.Transport == nil {
		t.Fatal("the node should start with a route")
	}

	// A renewal naming a certificate authority that does not exist: everything
	// about the request is fine except that its transport cannot be built.
	broken := directRequest("nas02")
	broken.LeaseSecret = res.LeaseSecret
	if _, err := r.RegisterWithDial(broken, netx.Options{
		CAFile: filepath.Join(t.TempDir(), "absent.pem"),
	}); err == nil {
		t.Fatal("a renewal that cannot build its transport should fail")
	}

	after, ok := r.Node("nas02")
	if !ok {
		t.Fatal("the lease disappeared")
	}
	if after.Transport == nil {
		t.Fatal("a failed renewal took away the route that was working")
	}
	if after.Info.Generation != before.Info.Generation {
		t.Fatalf("a failed renewal moved the generation: %d -> %d",
			before.Info.Generation, after.Info.Generation)
	}
	if !after.Info.Online {
		t.Fatal("a failed renewal took the node offline")
	}
}
