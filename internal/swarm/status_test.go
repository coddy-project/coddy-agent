package swarm

import (
	"context"
	"net/http"
	"slices"
	"strings"
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
