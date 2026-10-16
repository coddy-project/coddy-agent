package serve

import (
	"reflect"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/swarm"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

func TestBuildOf(t *testing.T) {
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "-tags", Value: "http,ui,metrics"},
		{Key: "vcs.revision", Value: "7b566ae3"},
		{Key: "CGO_ENABLED", Value: "0"},
	}}
	got := buildOf(info)
	want := Build{
		Version: version.Get(), Revision: "7b566ae3", GoVersion: runtime.Version(),
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Tags: []string{"http", "ui", "metrics"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildOf = %+v, want %+v", got, want)
	}

	// A binary without version control data or tags, or without build info at
	// all, still says what it is.
	for _, info := range []*debug.BuildInfo{nil, {}} {
		got := buildOf(info)
		if got.Revision != "" || got.Tags != nil || got.Version != version.Get() || got.GoVersion != runtime.Version() {
			t.Errorf("buildOf(%v) = %+v", info, got)
		}
	}
}

// The quota windows of the cached usage become one row per provider and
// window: the share used as a ratio and the moment of the reset. A snapshot
// that is stale, failed, unsupported or switched off says nothing current and
// is left out, and so is a window without an identifier.
func TestProviderQuotasFromTheCachedUsage(t *testing.T) {
	got := providerQuotas(map[string]acp.ProviderUsageUpdate{
		"hub": {Windows: []acp.UsageWindow{
			{ID: "session", UsedPercent: 25, ResetsAt: "2026-10-15T18:00:00Z"},
			{ID: "week", UsedPercent: 100, Exhausted: true},
			{ID: "", UsedPercent: 50},
		}},
		"stale":       {Stale: true, Windows: []acp.UsageWindow{{ID: "day", UsedPercent: 10}}},
		"failed":      {Error: "unavailable", Windows: []acp.UsageWindow{{ID: "day", UsedPercent: 10}}},
		"unsupported": {Unsupported: true},
		"off":         {Disabled: true, Windows: []acp.UsageWindow{{ID: "day", UsedPercent: 10}}},
	})
	want := []ProviderQuota{
		{Provider: "hub", Window: "session", UsedRatio: 0.25, ResetsAt: time.Date(2026, 10, 15, 18, 0, 0, 0, time.UTC)},
		{Provider: "hub", Window: "week", UsedRatio: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("quotas = %+v, want %+v", got, want)
	}
}

// A relay's registry is counted by kind, transport and state, every
// combination present so a quiet relay reads zeros; a kind or transport
// outside the closed set is not counted.
func TestRelayNodeCounts(t *testing.T) {
	got := relayNodeCounts([]swarm.NodeInfo{
		{Name: "a", Kind: swarm.KindAgent, Transport: swarm.TransportDirect, Online: true},
		{Name: "b", Kind: swarm.KindAgent, Transport: swarm.TransportTunnel, Online: true},
		{Name: "c", Kind: swarm.KindAgent, Transport: swarm.TransportTunnel, Online: false},
		{Name: "d", Kind: swarm.KindRelay, Transport: swarm.TransportDirect, Online: true},
		{Name: "e", Kind: "toaster", Transport: swarm.TransportDirect, Online: true},
	})
	if len(got) != 8 {
		t.Fatalf("%d rows, want every kind, transport and state: %+v", len(got), got)
	}
	count := func(kind, transport, state string) int {
		for _, r := range got {
			if r.Kind == kind && r.Transport == transport && r.State == state {
				return r.Count
			}
		}
		t.Fatalf("no row for %s/%s/%s", kind, transport, state)
		return -1
	}
	for _, tc := range []struct {
		kind, transport, state string
		want                   int
	}{
		{swarm.KindAgent, swarm.TransportDirect, "online", 1},
		{swarm.KindAgent, swarm.TransportTunnel, "online", 1},
		{swarm.KindAgent, swarm.TransportTunnel, "offline", 1},
		{swarm.KindRelay, swarm.TransportDirect, "online", 1},
		{swarm.KindRelay, swarm.TransportTunnel, "offline", 0},
	} {
		if n := count(tc.kind, tc.transport, tc.state); n != tc.want {
			t.Errorf("%s/%s/%s = %d, want %d", tc.kind, tc.transport, tc.state, n, tc.want)
		}
	}
}

// The snapshot reads the swarm state the process published and the prompts
// that wait; a process that runs no relay has no relay rows.
func TestRuntimeStatusSwarmAndPrompts(t *testing.T) {
	rt := &Runtime{}
	if snap := rt.Status(); snap.RelayNodes != nil || snap.PermissionPrompts != 0 || snap.Questions != 0 {
		t.Fatalf("an idle process: relay %+v, prompts %d/%d", snap.RelayNodes, snap.PermissionPrompts, snap.Questions)
	}
	withdraw := swarm.PublishRelay(func() []swarm.NodeInfo {
		return []swarm.NodeInfo{{Name: "a", Kind: swarm.KindAgent, Transport: swarm.TransportDirect, Online: true}}
	})
	defer withdraw()
	if snap := rt.Status(); len(snap.RelayNodes) != 8 {
		t.Fatalf("relay rows = %+v, want 8", snap.RelayNodes)
	}
}
