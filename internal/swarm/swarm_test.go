package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestValidateNodeName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"plain", "nas02", true},
		{"digits and dashes", "gpu-03_b", true},
		{"single char", "a", true},
		{"empty", "", false},
		{"space", "nas 02", false},
		{"slash", "nas/02", false},
		{"encoded slash", "nas%2F02", false},
		{"dot segment", "..", false},
		{"leading dot", ".hidden", false},
		{"unicode", "узел", false},
		{"too long", strings.Repeat("a", 65), false},
		{"max length", strings.Repeat("a", 64), true},
		{"reserved nodes", "nodes", false},
		{"reserved info", "info", false},
		{"reserved register", "register", false},
		{"reserved tunnel", "tunnel", false},
		{"reserved sessions", "sessions", false},
		{"reserved topology", "topology", false},
		{"reserved is case-insensitive", "Nodes", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateNodeName(tc.in)
			if tc.ok && err != nil {
				t.Fatalf("ValidateNodeName(%q) = %v, want nil", tc.in, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("ValidateNodeName(%q) = nil, want an error", tc.in)
			}
		})
	}
}

// A session reference addresses one session inside a swarm. Node names cannot
// contain a separator, so the last segment is always the session id and
// everything before it is the path of nodes leading to the owning agent. That
// rule is what keeps a multi-hop reference unambiguous.
func TestParseSessionRef(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		path    []string
		id      string
		wantErr bool
	}{
		{"bare id", "sess_abc", nil, "sess_abc", false},
		{"one hop", "nas02/sess_abc", []string{"nas02"}, "sess_abc", false},
		{"three hops", "relay2/relay3/agent7/sess_abc", []string{"relay2", "relay3", "agent7"}, "sess_abc", false},
		{"empty", "", nil, "", true},
		{"trailing separator", "nas02/", nil, "", true},
		{"leading separator", "/sess_abc", nil, "", true},
		{"empty middle segment", "a//sess", nil, "", true},
		{"invalid node name", "na s/sess_abc", nil, "", true},
		{"invalid session id", "nas02/sess abc", nil, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := ParseSessionRef(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseSessionRef(%q) = %+v, want an error", tc.in, ref)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSessionRef(%q): %v", tc.in, err)
			}
			if ref.ID != tc.id {
				t.Fatalf("id = %q, want %q", ref.ID, tc.id)
			}
			if strings.Join(ref.NodePath, "/") != strings.Join(tc.path, "/") {
				t.Fatalf("node path = %v, want %v", ref.NodePath, tc.path)
			}
		})
	}
}

func TestSessionRefRoundTrip(t *testing.T) {
	for _, in := range []string{"sess_abc", "nas02/sess_abc", "r2/r3/agent7/sess_abc"} {
		ref, err := ParseSessionRef(in)
		if err != nil {
			t.Fatalf("ParseSessionRef(%q): %v", in, err)
		}
		if got := ref.String(); got != in {
			t.Fatalf("round trip: %q -> %q", in, got)
		}
	}
}

// The mount prefix is what a client prepends to reach one node through a relay.
// It has to survive a multi-hop path without collapsing the hops.
func TestSessionRefMountPrefix(t *testing.T) {
	ref, err := ParseSessionRef("relay2/agent7/sess_abc")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ref.MountPrefix(), "/swarm/nodes/relay2/swarm/nodes/agent7"; got != want {
		t.Fatalf("MountPrefix() = %q, want %q", got, want)
	}
	local, err := ParseSessionRef("sess_abc")
	if err != nil {
		t.Fatal(err)
	}
	if got := local.MountPrefix(); got != "" {
		t.Fatalf("a local session needs no mount prefix, got %q", got)
	}
}

func TestValidateAdvertiseURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"http host port", "http://nas02:12345", true},
		{"https", "https://agent.example", true},
		{"with path", "http://gw.example/coddy-node", true},
		{"empty", "", false},
		{"no scheme", "nas02:12345", false},
		{"unsupported scheme", "ftp://nas02", false},
		{"query", "http://nas02:12345?x=1", false},
		{"fragment", "http://nas02:12345#f", false},
		{"credentials", "http://user:pw@nas02:12345", false},
		{"no host", "http://", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateAdvertiseURL(tc.in)
			if tc.ok && err != nil {
				t.Fatalf("ValidateAdvertiseURL(%q) = %v, want nil", tc.in, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("ValidateAdvertiseURL(%q) = nil, want an error", tc.in)
			}
		})
	}
}

// A registration document describes how the relay may reach the node. The two
// transports disagree about what must be present, so validation is what keeps
// a half-filled document from reaching the registry.
func TestRegisterRequestValidate(t *testing.T) {
	base := func() RegisterRequest {
		return RegisterRequest{
			Name:         "nas02",
			Kind:         KindAgent,
			Transport:    TransportDirect,
			AdvertiseURL: "http://nas02:12345",
			InstanceUUID: "11111111-1111-1111-1111-111111111111",
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("a complete direct registration should validate: %v", err)
	}

	tunnel := base()
	tunnel.Transport = TransportTunnel
	tunnel.AdvertiseURL = ""
	if err := tunnel.Validate(); err != nil {
		t.Fatalf("a tunnel registration needs no advertise url: %v", err)
	}

	bad := []struct {
		name   string
		mutate func(*RegisterRequest)
	}{
		{"no name", func(r *RegisterRequest) { r.Name = "" }},
		{"bad name", func(r *RegisterRequest) { r.Name = "nas/02" }},
		{"unknown kind", func(r *RegisterRequest) { r.Kind = "robot" }},
		{"unknown transport", func(r *RegisterRequest) { r.Transport = "carrier-pigeon" }},
		{"direct without url", func(r *RegisterRequest) { r.AdvertiseURL = "" }},
		{"direct with bad url", func(r *RegisterRequest) { r.AdvertiseURL = "ftp://nas02" }},
		{"no instance uuid", func(r *RegisterRequest) { r.InstanceUUID = "" }},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			req := base()
			tc.mutate(&req)
			if err := req.Validate(); err == nil {
				t.Fatalf("%+v should not validate", req)
			}
		})
	}
}

// ---- join client ----

// fakeRelay stands in for the registration half of a relay.
type fakeRelay struct {
	mu       sync.Mutex
	pairing  string
	leases   map[string]string
	requests []RegisterRequest
	status   int
	ts       *httptest.Server
}

func newFakeRelay(t *testing.T, pairing string) *fakeRelay {
	t.Helper()
	f := &fakeRelay{pairing: pairing, leases: map[string]string{}}
	f.ts = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.ts.Close)
	return f
}

func (f *fakeRelay) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/swarm/register" {
		http.NotFound(w, r)
		return
	}
	if got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); got != f.pairing {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, req)
	forced := f.status
	held, exists := f.leases[req.Name]
	if !exists {
		held = "lease-" + req.Name
		f.leases[req.Name] = held
	}
	generation := uint64(len(f.requests))
	f.mu.Unlock()

	if forced != 0 {
		w.WriteHeader(forced)
		return
	}
	if exists && req.LeaseSecret != held {
		w.WriteHeader(http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RegisterResponse{
		NodeID: req.Name, LeaseSecret: held, TTLSeconds: 90, Generation: generation,
	})
}

func (f *fakeRelay) seen() []RegisterRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]RegisterRequest(nil), f.requests...)
}

func (f *fakeRelay) forceStatus(code int) {
	f.mu.Lock()
	f.status = code
	f.mu.Unlock()
}

func TestJoinClientRegistersAndRemembersItsLease(t *testing.T) {
	relay := newFakeRelay(t, "pair")
	store := &MemorySecretStore{}
	c, err := NewClient(JoinOptions{
		RelayURL:     relay.ts.URL,
		Name:         "nas02",
		PairingToken: "pair",
		AdvertiseURL: "http://nas02:12345",
		NodeToken:    "node-token",
		Secrets:      store,
		Log:          quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !c.Online() {
		t.Fatal("a successful registration should read as online")
	}
	if c.LeaseSecret() != "lease-nas02" {
		t.Fatalf("lease secret = %q", c.LeaseSecret())
	}
	if got, ok := store.Load(relay.ts.URL, "nas02"); !ok || got != "lease-nas02" {
		t.Fatalf("the lease secret was not persisted: %q %v", got, ok)
	}

	// A heartbeat presents the secret, which is what keeps the name.
	if err := c.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	seen := relay.seen()
	if len(seen) != 2 {
		t.Fatalf("relay saw %d registrations", len(seen))
	}
	if seen[0].LeaseSecret != "" {
		t.Fatal("the first registration should claim the name without a secret")
	}
	if seen[1].LeaseSecret != "lease-nas02" {
		t.Fatalf("the heartbeat should present the secret, got %q", seen[1].LeaseSecret)
	}
}

// A node that restarts must re-claim its own name at once, not wait out the
// lease it left behind.
func TestJoinClientReusesAStoredLeaseAcrossRestarts(t *testing.T) {
	relay := newFakeRelay(t, "pair")
	store := &MemorySecretStore{}
	opts := JoinOptions{
		RelayURL: relay.ts.URL, Name: "nas02", PairingToken: "pair",
		AdvertiseURL: "http://nas02:12345", Secrets: store, Log: quietLogger(),
	}
	first, err := NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Register(context.Background()); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.LeaseSecret() != "lease-nas02" {
		t.Fatal("a restarted node should load the secret it stored")
	}
	if err := restarted.Register(context.Background()); err != nil {
		t.Fatalf("a restarted node should re-claim its own name: %v", err)
	}
}

func TestJoinClientReportsANameConflictDistinctly(t *testing.T) {
	relay := newFakeRelay(t, "pair")
	c, err := NewClient(JoinOptions{
		RelayURL: relay.ts.URL, Name: "nas02", PairingToken: "pair",
		AdvertiseURL: "http://nas02:12345", Log: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	relay.forceStatus(http.StatusConflict)
	err = c.Register(context.Background())
	if !errors.Is(err, ErrNameConflict) {
		t.Fatalf("err = %v, want ErrNameConflict", err)
	}
	if c.Online() {
		t.Fatal("a refused node must not read as online")
	}
}

func TestJoinClientRejectsABadPairingToken(t *testing.T) {
	relay := newFakeRelay(t, "pair")
	c, err := NewClient(JoinOptions{
		RelayURL: relay.ts.URL, Name: "nas02", PairingToken: "wrong",
		AdvertiseURL: "http://nas02:12345", Log: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Register(context.Background()); err == nil {
		t.Fatal("a wrong pairing token should not register")
	}
}

// Leaving the advertised URL out is how a node says it cannot be dialled and
// will open the connection itself.
func TestJoinClientPicksTheTransportFromTheAdvertisedURL(t *testing.T) {
	relay := newFakeRelay(t, "pair")
	direct, err := NewClient(JoinOptions{RelayURL: relay.ts.URL, Name: "a", AdvertiseURL: "http://a:1", Log: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if direct.Transport() != TransportDirect {
		t.Fatalf("transport = %q, want direct", direct.Transport())
	}
	tunnel, err := NewClient(JoinOptions{RelayURL: relay.ts.URL, Name: "b", Log: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if tunnel.Transport() != TransportTunnel {
		t.Fatalf("transport = %q, want tunnel", tunnel.Transport())
	}
}

func TestNewClientValidates(t *testing.T) {
	if _, err := NewClient(JoinOptions{Name: "a"}); err == nil {
		t.Fatal("a client without a relay url should not be built")
	}
	if _, err := NewClient(JoinOptions{RelayURL: "http://r", Name: "bad name"}); err == nil {
		t.Fatal("an invalid node name should not be accepted")
	}
}

// Run keeps the node registered; cancelling stops it promptly.
func TestJoinClientRunHeartbeatsUntilCancelled(t *testing.T) {
	relay := newFakeRelay(t, "pair")
	c, err := NewClient(JoinOptions{
		RelayURL: relay.ts.URL, Name: "nas02", PairingToken: "pair",
		AdvertiseURL: "http://nas02:12345", Log: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	deadline := time.After(3 * time.Second)
	for len(relay.seen()) == 0 || !c.Online() {
		select {
		case <-deadline:
			t.Fatal("the client never registered")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop when its context was cancelled")
	}
}

// A relay restart wakes every node it served at the same moment, so retries
// must not line up.
func TestJitterSpreadsRetries(t *testing.T) {
	const nominal = time.Second
	seen := map[time.Duration]int{}
	for i := 0; i < 200; i++ {
		d := jitter(nominal)
		if d < nominal/2 || d > nominal {
			t.Fatalf("jitter(%v) = %v, outside [50%%, 100%%]", nominal, d)
		}
		seen[d]++
	}
	if len(seen) < 50 {
		t.Fatalf("jitter produced only %d distinct waits out of 200; retries would still cluster", len(seen))
	}
	if jitter(0) != 0 {
		t.Fatal("jitter(0) should stay 0")
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	d := initialBackoff
	for i := 0; i < 20; i++ {
		next := nextBackoff(d)
		if next < d {
			t.Fatalf("backoff went backwards: %v -> %v", d, next)
		}
		d = next
	}
	if d != maxBackoff {
		t.Fatalf("backoff settled at %v, want the %v cap", d, maxBackoff)
	}
}

func TestSanitiseNodeNameMakesAHostNameRoutable(t *testing.T) {
	cases := map[string]string{
		"nas02.local":  "nas02-local",
		"GPU_03":       "GPU_03",
		"héllo.host":   "hllo-host",
		"weird!!@name": "weirdname",
	}
	for in, want := range cases {
		if got := sanitiseNodeName(in); got != want {
			t.Fatalf("sanitiseNodeName(%q) = %q, want %q", in, got, want)
		}
	}
	if err := ValidateNodeName(sanitiseNodeName("nas02.local")); err != nil {
		t.Fatalf("a sanitised host name should be a valid node name: %v", err)
	}
}

func TestFileSecretStoreRoundTrips(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSecretStore(dir)
	if _, ok := store.Load("http://relay", "nas02"); ok {
		t.Fatal("an empty store should hold nothing")
	}
	if err := store.Save("http://relay", "nas02", "s3cret"); err != nil {
		t.Fatal(err)
	}
	reopened := NewFileSecretStore(dir)
	got, ok := reopened.Load("http://relay", "nas02")
	if !ok || got != "s3cret" {
		t.Fatalf("reopened store returned %q %v", got, ok)
	}
	// Credentials on disk are owner-only.
	info, err := os.Stat(filepath.Join(dir, "swarm-leases.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("lease file mode = %o, want 600", perm)
	}
}

func TestFileSecretStoreSurvivesACorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "swarm-leases.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewFileSecretStore(dir)
	if _, ok := store.Load("http://relay", "nas02"); ok {
		t.Fatal("a corrupt file should read as empty, not as a secret")
	}
	if err := store.Save("http://relay", "nas02", "fresh"); err != nil {
		t.Fatalf("a corrupt file should not block a new secret: %v", err)
	}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// A relay that accepts the connection and then says nothing must not hold the
// node - or a shutdown waiting on it - forever.
func TestDialTunnelGivesUpOnASilentRelay(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			// Accept and say nothing at all.
			go func() { <-time.After(30 * time.Second); _ = c.Close() }()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- DialTunnel(ctx, TunnelOptions{
			RelayURL:    "http://" + ln.Addr().String(),
			Node:        "inner",
			LeaseSecret: "s",
			Handler:     http.NotFoundHandler(),
		})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a silent relay should not look like a successful tunnel")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DialTunnel hung on a relay that never answered")
	}
}

// Cancelling has to reach a blocking read, which only closing the connection
// does.
func TestDialTunnelStopsWhenItsContextIsCancelled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func() { <-time.After(60 * time.Second); _ = c.Close() }()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- DialTunnel(ctx, TunnelOptions{
			RelayURL:    "http://" + ln.Addr().String(),
			Node:        "inner",
			LeaseSecret: "s",
			Handler:     http.NotFoundHandler(),
		})
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled dial kept waiting on a blocking read")
	}
}

// One identity per process. A node joining two relays under two identities
// would defeat the deduplication a ring relies on.
func TestStartJoinsGivesEveryParentTheSameIdentity(t *testing.T) {
	relay := newFakeRelay(t, "pair")
	cfg := &config.Config{}
	cfg.Swarm.Join = []config.SwarmJoin{
		{URL: relay.ts.URL, Name: "node-a", PairingToken: "pair", AdvertiseURL: "http://a:1"},
		{URL: relay.ts.URL, Name: "node-b", PairingToken: "pair", AdvertiseURL: "http://b:1"},
	}
	set, err := StartJoins(context.Background(), cfg, StartJoinsOptions{
		Kind: KindAgent, Handler: http.NotFoundHandler(), Log: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Stop()

	clients := set.Clients()
	if len(clients) != 2 {
		t.Fatalf("expected two join clients, got %d", len(clients))
	}
	first := clients[0].opts.InstanceUUID
	if first == "" {
		t.Fatal("no identity was generated")
	}
	if clients[1].opts.InstanceUUID != first {
		t.Fatalf("two parents saw two identities: %q and %q", first, clients[1].opts.InstanceUUID)
	}
}
