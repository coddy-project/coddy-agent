package swarm

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// A running join set is reported, registration by registration, for as long
// as it runs: the parent relay by the host of its URL, never a credential.
func TestJoinsReportTheRunningRegistrations(t *testing.T) {
	cfg := &config.Config{}
	cfg.Swarm.Join = []config.SwarmJoin{
		{URL: "http://user:secret@127.0.0.1:1/base?token=x", Name: "alpha", PairingToken: "pair"},
		{URL: "https://relay.example:8443", Name: "alpha", PairingToken: "pair", AdvertiseURL: "http://127.0.0.1:2"},
	}
	set, err := StartJoins(context.Background(), cfg, StartJoinsOptions{Kind: KindAgent})
	if err != nil {
		t.Fatal(err)
	}
	got := Joins()
	want := []JoinStatus{
		{Relay: "127.0.0.1:1", Kind: KindAgent, Transport: TransportTunnel},
		{Relay: "relay.example:8443", Kind: KindAgent, Transport: TransportDirect},
	}
	for _, w := range want {
		if !slices.ContainsFunc(got, func(j JoinStatus) bool { return j.Relay == w.Relay && j.Kind == w.Kind && j.Transport == w.Transport }) {
			t.Errorf("joins = %+v, want one like %+v", got, w)
		}
	}
	set.Stop()
	for _, j := range Joins() {
		if j.Relay == "127.0.0.1:1" || j.Relay == "relay.example:8443" {
			t.Errorf("a stopped join set is still reported: %+v", j)
		}
	}
}

// The relay this process runs is readable while it is published, and only
// then.
func TestRelayNodesWhileTheRelayIsPublished(t *testing.T) {
	if _, ok := RelayNodes(); ok {
		t.Fatal("a relay is reported before any was published")
	}
	nodes := []NodeInfo{{Name: "alpha", Kind: KindAgent, Transport: TransportDirect, Online: true}}
	withdraw := PublishRelay(func() []NodeInfo { return nodes })
	got, ok := RelayNodes()
	if !ok || len(got) != 1 || got[0].Name != "alpha" {
		t.Fatalf("relay nodes = %+v, %v", got, ok)
	}
	withdraw()
	withdraw()
	if _, ok := RelayNodes(); ok {
		t.Fatal("a withdrawn relay is still reported")
	}
}

// A process that runs both an agent API and a relay starts two join sets from
// the same swarm.join (the API's as an agent, the relay's as a relay) that
// share the lease of one name. While the relay registers the process, the
// agent registration stays quiet, so the parent keeps one node of one kind
// instead of a row that flips between the two; the process reports that one
// registration. Once the relay stops, the agent registers again.
func TestAnAgentYieldsToTheRelayOfItsOwnProcess(t *testing.T) {
	relay := newFakeRelay(t, "pair")
	home := t.TempDir()
	cfg := &config.Config{}
	cfg.Swarm.Join = []config.SwarmJoin{{URL: relay.ts.URL, Name: "both", PairingToken: "pair", AdvertiseURL: "http://both:12345"}}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting until %s; registrations %+v", what, relay.seen())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	kinds := func(reqs []RegisterRequest) []string {
		var out []string
		for _, r := range reqs {
			out = append(out, r.Kind)
		}
		return out
	}

	relaySet, err := StartJoins(context.Background(), cfg, StartJoinsOptions{Kind: KindRelay, Home: home, Handler: http.NotFoundHandler(), Log: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	waitFor("the relay registered", func() bool { return len(relay.seen()) == 1 })
	agentSet, err := StartJoins(context.Background(), cfg, StartJoinsOptions{Kind: KindAgent, Home: home, Handler: http.NotFoundHandler(), Log: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	defer agentSet.Stop()

	time.Sleep(1500 * time.Millisecond)
	if got := kinds(relay.seen()); !slices.Equal(got, []string{KindRelay}) {
		t.Fatalf("registrations = %v, want the relay's alone while it runs", got)
	}
	var mine []JoinStatus
	for _, j := range Joins() {
		if j.Relay == strings.TrimPrefix(relay.ts.URL, "http://") {
			mine = append(mine, j)
		}
	}
	if len(mine) != 1 || mine[0].Kind != KindRelay || !mine[0].Online {
		t.Fatalf("the process reports %+v, want the relay's registration alone, online", mine)
	}

	relaySet.Stop()
	waitFor("the agent registered once the relay stopped", func() bool {
		seen := relay.seen()
		return len(seen) >= 2 && seen[len(seen)-1].Kind == KindAgent
	})
}

// fakeClock is a clock a test moves by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.mu.Unlock()
}

// newJoinClient is a direct registration into relay, not started.
func newJoinClient(t *testing.T, relay *fakeRelay, kind, name string) *Client {
	t.Helper()
	c, err := NewClient(JoinOptions{
		RelayURL: relay.ts.URL, Name: name, Kind: kind, PairingToken: "pair",
		AdvertiseURL: "http://" + name + ":12345", Log: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// outageOf reads a client the way Joins does, and fails when the three parts
// of the read disagree with each other.
func outageOf(t *testing.T, c *Client) (online bool, since time.Time) {
	t.Helper()
	yielding, online, since := c.status()
	if yielding {
		t.Fatalf("client %s is yielding", c.Name())
	}
	return online, since
}

// A registration is offline from the moment it is created, since it has not
// registered yet; a failure after a success starts an outage at that failure
// and later failures keep its start; a success ends it.
func TestOfflineSinceFollowsTheOutage(t *testing.T) {
	relay := newFakeRelay(t, "pair")
	before := time.Now()
	c := newJoinClient(t, relay, KindAgent, "alpha")
	after := time.Now()

	online, since := outageOf(t, c)
	if online || since.Before(before) || since.After(after) {
		t.Fatalf("a new client reads online = %v, offline since %v; want offline since it was created, between %v and %v", online, since, before, after)
	}

	// A failed attempt does not move the start of an outage that began with
	// the client.
	clock := newFakeClock()
	c.now = clock.Now
	clock.Advance(time.Hour)
	gone, err := NewClient(JoinOptions{RelayURL: "http://127.0.0.1:1", Name: "gone", AdvertiseURL: "http://gone:1", Log: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	_, goneSince := outageOf(t, gone)
	gone.now = clock.Now
	if err := gone.Register(context.Background()); err == nil {
		t.Fatal("a registration into a relay that is not there reported no error")
	}
	if online, since = outageOf(t, gone); online || !since.Equal(goneSince) {
		t.Errorf("a first failure of a client that never registered: online = %v, since %v; want the creation, %v", online, since, goneSince)
	}

	relay.forceStatus(0)
	if err := c.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	if online, since = outageOf(t, c); !online || !since.IsZero() {
		t.Errorf("after a success: online = %v, since %v; want online, no outage", online, since)
	}

	clock.Advance(time.Minute)
	failedAt := clock.Now()
	relay.forceStatus(http.StatusServiceUnavailable)
	_ = c.Register(context.Background())
	if online, since = outageOf(t, c); online || !since.Equal(failedAt) {
		t.Errorf("a failure after a success: online = %v, since %v; want offline since %v", online, since, failedAt)
	}
	clock.Advance(2 * time.Minute)
	relay.forceStatus(http.StatusConflict)
	_ = c.Register(context.Background())
	clock.Advance(time.Minute)
	_ = c.Register(context.Background())
	if _, since = outageOf(t, c); !since.Equal(failedAt) {
		t.Errorf("later failures moved the start of the outage to %v, want %v", since, failedAt)
	}

	relay.forceStatus(0)
	if err := c.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	if online, since = outageOf(t, c); !online || !since.IsZero() {
		t.Errorf("after the recovery: online = %v, since %v; want online, no outage", online, since)
	}
	clock.Advance(time.Minute)
	relay.forceStatus(http.StatusServiceUnavailable)
	_ = c.Register(context.Background())
	if _, since = outageOf(t, c); !since.Equal(clock.Now()) {
		t.Errorf("a second outage began at %v, want %v", since, clock.Now())
	}
}

// An agent that yields to the relay of its own process is no outage: entering
// the yield clears the outage, leaving it starts a fresh interval at that
// moment, so a takeover cannot count the minutes spent yielding, and a client
// that yields is still left out of Joins.
func TestOfflineSinceStartsFreshWhenAYieldEnds(t *testing.T) {
	relay := newFakeRelay(t, "pair")
	clock := newFakeClock()

	relayClient := newJoinClient(t, relay, KindRelay, "both")
	relaySet := &JoinSet{clients: []*Client{relayClient}}
	registerJoinSet(relaySet)
	defer relaySet.Stop()

	agent := newJoinClient(t, relay, KindAgent, "both")
	agent.now = clock.Now
	agentSet := &JoinSet{clients: []*Client{agent}}
	registerJoinSet(agentSet)
	defer agentSet.Stop()

	host := relayHost(relay.ts.URL)
	mine := func() []JoinStatus {
		var out []JoinStatus
		for _, j := range Joins() {
			if j.Relay == host {
				out = append(out, j)
			}
		}
		return out
	}
	if got := mine(); len(got) != 2 {
		t.Fatalf("before the yield the process reports %+v, want both registrations", got)
	}

	if !agent.yieldsToRelay() {
		t.Fatal("the agent does not yield to the relay of its own process")
	}
	if yielding, online, since := agent.status(); !yielding || online || !since.IsZero() {
		t.Errorf("while yielding: yielding = %v, online = %v, since %v; want yielding, offline, no outage", yielding, online, since)
	}
	if got := mine(); len(got) != 1 || got[0].Kind != KindRelay {
		t.Errorf("while yielding the process reports %+v, want the relay's registration alone", got)
	}

	clock.Advance(10 * time.Minute)
	relaySet.Stop()
	left := clock.Now()
	if agent.yieldsToRelay() {
		t.Fatal("the agent still yields after its relay stopped")
	}
	if yielding, online, since := agent.status(); yielding || online || !since.Equal(left) {
		t.Errorf("after the yield: yielding = %v, online = %v, since %v; want an outage that began when the yield ended, %v", yielding, online, since, left)
	}

	// Failures after the yield keep that start.
	relay.forceStatus(http.StatusServiceUnavailable)
	clock.Advance(4*time.Minute + 59*time.Second)
	_ = agent.Register(context.Background())
	if _, since := outageOf(t, agent); !since.Equal(left) || clock.Now().Sub(since) >= 5*time.Minute {
		t.Errorf("4m59s after the yield ended, the outage began at %v, want %v", since, left)
	}
	got := mine()
	if len(got) != 1 || got[0].Kind != KindAgent || got[0].Online || !got[0].OfflineSince.Equal(left) {
		t.Errorf("after the yield the process reports %+v, want the agent offline since %v", got, left)
	}
}

// A join set a configuration reload replaces leaves nothing behind: the
// process reports the new registrations only, each offline since its own
// creation.
func TestJoinsAfterAJoinSetIsReplaced(t *testing.T) {
	join := func(urls ...string) *config.Config {
		cfg := &config.Config{}
		for _, u := range urls {
			cfg.Swarm.Join = append(cfg.Swarm.Join, config.SwarmJoin{URL: u, Name: "alpha", PairingToken: "pair"})
		}
		return cfg
	}
	statuses := func() map[string]JoinStatus {
		out := map[string]JoinStatus{}
		for _, j := range Joins() {
			out[j.Relay] = j
		}
		return out
	}

	first, err := StartJoins(context.Background(), join("http://127.0.0.1:1"), StartJoinsOptions{Kind: KindAgent, Log: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	firstSeen, ok := statuses()["127.0.0.1:1"]
	if !ok || firstSeen.Online || firstSeen.OfflineSince.IsZero() {
		t.Fatalf("the first set reports %+v, want one registration, offline since its creation", firstSeen)
	}
	first.Stop()
	time.Sleep(20 * time.Millisecond)

	before := time.Now()
	second, err := StartJoins(context.Background(), join("http://127.0.0.1:2", "http://127.0.0.1:3"), StartJoinsOptions{Kind: KindAgent, Log: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Stop()
	after := time.Now()

	got := statuses()
	if _, stale := got["127.0.0.1:1"]; stale {
		t.Errorf("the replaced set is still reported: %+v", got["127.0.0.1:1"])
	}
	for _, relay := range []string{"127.0.0.1:2", "127.0.0.1:3"} {
		j, ok := got[relay]
		if !ok {
			t.Errorf("the new set's registration into %s is not reported; got %+v", relay, got)
			continue
		}
		if j.Online || j.OfflineSince.Before(before) || j.OfflineSince.After(after) || !j.OfflineSince.After(firstSeen.OfflineSince) {
			t.Errorf("%s: online = %v, offline since %v; want offline since its own creation, between %v and %v", relay, j.Online, j.OfflineSince, before, after)
		}
	}
}

// Whether a client is online and since when its outage runs are one fact:
// a reader that takes them in one call never sees a registration that
// succeeded between two reads as offline without a start.
func TestClientStatusIsReadUnderOneLock(t *testing.T) {
	relay := newFakeRelay(t, "pair")
	c := newJoinClient(t, relay, KindAgent, "alpha")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				relay.forceStatus(0)
			} else {
				relay.forceStatus(http.StatusServiceUnavailable)
			}
			_ = c.Register(context.Background())
		}
	}()
	defer func() {
		close(stop)
		wg.Wait()
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	for reads := 0; time.Now().Before(deadline); reads++ {
		yielding, online, since := c.status()
		if yielding || online != since.IsZero() {
			t.Fatalf("read %d: yielding = %v, online = %v, offline since %v: an online client has no outage and an offline one has a start", reads, yielding, online, since)
		}
	}
}
