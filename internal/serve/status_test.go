package serve

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
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

// The build names its revision (version.Revision, set by -ldflags) because the
// toolchain stamps a worktree nested in another checkout with the enclosing
// checkout's commit; that revision wins over vcs.revision, and a build that
// does not pass it keeps reading vcs.revision.
func TestBuildOfPrefersTheRevisionTheBuildNames(t *testing.T) {
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "7b566ae3"},
	}}
	for _, tc := range []struct {
		name, named string
		info        *debug.BuildInfo
		want        string
	}{
		{"named, stamped by the enclosing checkout", "97be43ea", info, "97be43ea"},
		{"named, no build info", "97be43ea", nil, "97be43ea"},
		{"not named, stamped", "", info, "7b566ae3"},
		{"not named, not stamped", "", &debug.BuildInfo{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prev := version.Revision
			version.Revision = tc.named
			t.Cleanup(func() { version.Revision = prev })
			if got := buildOf(tc.info).Revision; got != tc.want {
				t.Errorf("Revision = %q, want %q", got, tc.want)
			}
		})
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

var docNow = time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)

func nan() float64 { return math.NaN() }

func healthySnapshot() Snapshot {
	return Snapshot{
		Subsystems: []SubsystemStatus{{Kind: KindHTTP, State: StateRunning}, {Kind: KindMetrics, State: StateRetained}},
		Disk:       &Disk{AvailableBytes: 10 << 30, SizeBytes: 100 << 30},
		SwarmJoins: []swarm.JoinStatus{{Relay: "relay.example:12346", Kind: swarm.KindAgent, Transport: swarm.TransportTunnel, Online: true}},
	}
}

// attentionOf is what RenderStatus puts in the document for a snapshot and a
// block, in the order it puts it.
func attentionOf(s Snapshot, m *MetricsBlock) []AttentionItem {
	return RenderStatus(s, m, docNow, "").Attention
}

func TestAttentionNothingToReport(t *testing.T) {
	if got := attentionOf(healthySnapshot(), &MetricsBlock{}); len(got) != 0 {
		t.Errorf("a healthy process needs attention: %+v", got)
	}
	if got := attentionOf(Snapshot{}, nil); len(got) != 0 {
		t.Errorf("an empty snapshot needs attention: %+v", got)
	}
}

// The flag of the snapshot is what raises the item, whatever made it true: a
// moved listener, or a surface that needs a session store in a process that
// has none. A refusal explains why nothing restarts; it is not a second problem.
func TestAttentionRestartRequired(t *testing.T) {
	moved := Snapshot{
		RestartRequired: true,
		Subsystems:      []SubsystemStatus{{Kind: KindHTTP, State: StateRunning, Address: "12345", PendingAddress: "23456"}},
	}
	sessionless := Snapshot{
		RestartRequired: true,
		Subsystems:      []SubsystemStatus{{Kind: KindSwarm, State: StateRunning}, {Kind: KindScheduler, State: StateStarting}},
	}
	for name, s := range map[string]Snapshot{"a moved listener": moved, "a surface without a store": sessionless, "the bare flag": {RestartRequired: true}} {
		if got, want := attentionOf(s, nil), []AttentionItem{{Code: AttentionRestartRequired}}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %+v, want %+v", name, got, want)
		}
	}
	if got := attentionOf(Snapshot{RestartRefused: "gateways.telegram.enable is true"}, nil); len(got) != 0 {
		t.Errorf("a refusal alone needs attention: %+v", got)
	}
}

func TestAttentionDiskLow(t *testing.T) {
	for _, tc := range []struct {
		name string
		disk *Disk
		low  bool
	}{
		{"one byte below", &Disk{AvailableBytes: DiskLowBytes - 1, SizeBytes: 1 << 40}, true},
		{"exactly the limit", &Disk{AvailableBytes: DiskLowBytes, SizeBytes: 1 << 40}, false},
		{"one byte above", &Disk{AvailableBytes: DiskLowBytes + 1, SizeBytes: 1 << 40}, false},
		{"full", &Disk{AvailableBytes: 0, SizeBytes: 1 << 40}, true},
		{"never read", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var want []AttentionItem
			if tc.low {
				want = []AttentionItem{{Code: AttentionDiskLow}}
			}
			if got := attentionOf(Snapshot{Disk: tc.disk}, nil); !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
	if DiskLowBytes != 512*1024*1024 {
		t.Errorf("DiskLowBytes = %d, want 512 MiB", int64(DiskLowBytes))
	}
}

func TestAttentionSubsystemDown(t *testing.T) {
	s := Snapshot{Subsystems: []SubsystemStatus{
		{Kind: KindHTTP, State: StateRunning},
		{Kind: KindGateway, State: StateStopped},
		{Kind: KindSwarm, State: StateStarting},
		{Kind: KindScheduler, State: StateRetained},
		{Kind: KindMetrics, State: StateStopped},
	}}
	want := []AttentionItem{
		{Code: AttentionSubsystemDown, Subsystem: KindGateway},
		{Code: AttentionSubsystemDown, Subsystem: KindMetrics},
	}
	if got := attentionOf(s, nil); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v: only a stopped row is down, in row order", got, want)
	}
}

func TestAttentionProviderFailing(t *testing.T) {
	failedAt := docNow.Add(-3 * time.Minute)
	for _, tc := range []struct {
		name    string
		stats   ProviderStats
		failing bool
	}{
		{"failed lately, never succeeded", ProviderStats{LastFailureAt: failedAt}, true},
		{"failed lately, succeeded before", ProviderStats{LastFailureAt: failedAt, LastOKAt: failedAt.Add(-time.Minute)}, true},
		{"succeeded after the failure", ProviderStats{LastFailureAt: failedAt, LastOKAt: failedAt.Add(time.Second)}, false},
		{"succeeded in the same sample", ProviderStats{LastFailureAt: failedAt, LastOKAt: failedAt}, false},
		{"failed exactly at the edge of the window", ProviderStats{LastFailureAt: docNow.Add(-ProviderFailingWindow)}, true},
		{"failed 16 minutes ago", ProviderStats{LastFailureAt: docNow.Add(-16 * time.Minute)}, false},
		{"never failed", ProviderStats{LastOKAt: failedAt}, false},
		{"no sample at all", ProviderStats{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.stats.Name = "local-mlx"
			var want []AttentionItem
			if tc.failing {
				want = []AttentionItem{{Code: AttentionProviderFailing, Provider: "local-mlx"}}
			}
			if got := attentionOf(Snapshot{}, &MetricsBlock{Providers: []ProviderStats{tc.stats}}); !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}

	t.Run("without a block there are no providers to judge", func(t *testing.T) {
		if got := attentionOf(Snapshot{}, nil); len(got) != 0 {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("by name, whatever order they come in", func(t *testing.T) {
		in := []ProviderStats{
			{Name: "zeta", LastFailureAt: failedAt},
			{Name: "fine", LastOKAt: failedAt},
			{Name: "alpha", LastFailureAt: failedAt},
		}
		want := []AttentionItem{{Code: AttentionProviderFailing, Provider: "alpha"}, {Code: AttentionProviderFailing, Provider: "zeta"}}
		if got := attentionOf(Snapshot{}, &MetricsBlock{Providers: in}); !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
		if in[0].Name != "zeta" || in[2].Name != "alpha" {
			t.Error("the document reordered the caller's slice")
		}
	})
}

// joinOffline is a registration into relay that went offline age before docNow.
func joinOffline(relay, kind string, age time.Duration) swarm.JoinStatus {
	return swarm.JoinStatus{
		Relay: relay, Kind: kind, Transport: swarm.TransportTunnel, OfflineSince: docNow.Add(-age),
	}
}

// A registration into a parent relay that has been offline for the grace of
// CoddySwarmJoinDown needs attention, once per relay.
func TestAttentionSwarmJoinDown(t *testing.T) {
	const relay = "relay.example:12346"
	item := func(relay string, age time.Duration) AttentionItem {
		return AttentionItem{Code: AttentionSwarmJoinDown, Relay: relay, OfflineSince: docNow.Add(-age)}
	}
	for _, tc := range []struct {
		name  string
		joins []swarm.JoinStatus
		want  []AttentionItem
	}{
		{"offline for 4m59s", []swarm.JoinStatus{joinOffline(relay, swarm.KindAgent, 5*time.Minute-time.Second)}, nil},
		{"offline for 5m", []swarm.JoinStatus{joinOffline(relay, swarm.KindAgent, 5*time.Minute)}, []AttentionItem{item(relay, 5*time.Minute)}},
		{"offline for an hour", []swarm.JoinStatus{joinOffline(relay, swarm.KindAgent, time.Hour)}, []AttentionItem{item(relay, time.Hour)}},
		{"online", []swarm.JoinStatus{{Relay: relay, Kind: swarm.KindAgent, Online: true}}, nil},
		{"no registration", nil, nil},
		{"offline with no start", []swarm.JoinStatus{{Relay: relay, Kind: swarm.KindAgent}}, nil},
		{
			"two kinds into one relay are one item, from the earlier outage",
			[]swarm.JoinStatus{joinOffline(relay, swarm.KindAgent, 7*time.Minute), joinOffline(relay, swarm.KindRelay, 20*time.Minute)},
			[]AttentionItem{item(relay, 20*time.Minute)},
		},
		{
			"one kind past the grace and one not",
			[]swarm.JoinStatus{joinOffline(relay, swarm.KindAgent, time.Minute), joinOffline(relay, swarm.KindRelay, 6*time.Minute)},
			[]AttentionItem{item(relay, 6*time.Minute)},
		},
		{
			"two relays are two items, by host",
			[]swarm.JoinStatus{joinOffline("zeta.example", swarm.KindAgent, 9*time.Minute), joinOffline("alpha.example:1", swarm.KindAgent, 6*time.Minute)},
			[]AttentionItem{item("alpha.example:1", 6*time.Minute), item("zeta.example", 9*time.Minute)},
		},
		{
			"a relay that is fine beside one that is not",
			[]swarm.JoinStatus{{Relay: "ok.example", Kind: swarm.KindAgent, Online: true}, joinOffline(relay, swarm.KindAgent, 8*time.Minute)},
			[]AttentionItem{item(relay, 8*time.Minute)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// With the exporter's block and without: the item is there in every build.
			for _, m := range []*MetricsBlock{nil, {}} {
				if got := attentionOf(Snapshot{SwarmJoins: tc.joins}, m); !reflect.DeepEqual(got, tc.want) {
					t.Errorf("block %v: got %+v, want %+v", m != nil, got, tc.want)
				}
			}
		})
	}

	t.Run("the item on the wire", func(t *testing.T) {
		since := time.Date(2026, 10, 15, 9, 48, 0, 0, time.FixedZone("CEST", 2*3600))
		s := Snapshot{SwarmJoins: []swarm.JoinStatus{{Relay: relay, Kind: swarm.KindAgent, OfflineSince: since}}}
		got, err := json.Marshal(attentionOf(s, nil))
		if err != nil {
			t.Fatal(err)
		}
		if want := `[{"code":"swarm_join_down","relay":"relay.example:12346","offlineSince":"2026-10-15T07:48:00Z"}]`; string(got) != want {
			t.Errorf("attention = %s, want %s", got, want)
		}
	})
}

// The fixed figures are the ones the operator's Prometheus carries in the
// shipped rules: the room CoddyDiskLow waits for, the window CoddyProviderFailing
// looks back over, and how long CoddySwarmJoinDown lets a registration stay
// offline. A rule moved without its constant (or the other way round) would
// show a different problem on the page than in the alert.
func TestTheFixedFiguresMatchTheShippedRules(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash("../../examples/prometheus/coddy-rules.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var rules struct {
		Groups []struct {
			Rules []struct {
				Alert string `yaml:"alert"`
				Expr  string `yaml:"expr"`
				For   string `yaml:"for"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(raw, &rules); err != nil {
		t.Fatal(err)
	}
	rule := func(alert string) (expr, wait string) {
		t.Helper()
		for _, g := range rules.Groups {
			for _, r := range g.Rules {
				if r.Alert == alert {
					return r.Expr, r.For
				}
			}
		}
		t.Fatalf("examples/prometheus/coddy-rules.yml has no %s", alert)
		return "", ""
	}

	t.Run("CoddySwarmJoinDown waits SwarmJoinGrace", func(t *testing.T) {
		expr, wait := rule("CoddySwarmJoinDown")
		if expr != `coddy_swarm_joins{state="offline"} > 0` {
			t.Errorf("CoddySwarmJoinDown expr = %q: SwarmJoinGrace was written for an offline registration", expr)
		}
		if d, err := time.ParseDuration(wait); err != nil || d != SwarmJoinGrace {
			t.Errorf("CoddySwarmJoinDown for = %q, SwarmJoinGrace = %v", wait, SwarmJoinGrace)
		}
	})

	t.Run("CoddyDiskLow fires below DiskLowBytes", func(t *testing.T) {
		expr, _ := rule("CoddyDiskLow")
		m := regexp.MustCompile(`^coddy_sessions_disk_available_bytes < (\d+(?: \* \d+)*)$`).FindStringSubmatch(strings.TrimSpace(expr))
		if m == nil {
			t.Fatalf("CoddyDiskLow expr = %q: DiskLowBytes was written for a limit on the available bytes", expr)
		}
		limit := int64(1)
		for _, factor := range strings.Split(m[1], " * ") {
			n, err := strconv.ParseInt(factor, 10, 64)
			if err != nil {
				t.Fatalf("CoddyDiskLow limit %q: %v", m[1], err)
			}
			limit *= n
		}
		if limit != DiskLowBytes {
			t.Errorf("CoddyDiskLow limit = %d bytes, DiskLowBytes = %d", limit, int64(DiskLowBytes))
		}
	})

	t.Run("CoddyProviderFailing looks back ProviderFailingWindow", func(t *testing.T) {
		expr, _ := rule("CoddyProviderFailing")
		ranges := regexp.MustCompile(`\[(\w+)\]`).FindAllStringSubmatch(expr, -1)
		if len(ranges) == 0 {
			t.Fatalf("CoddyProviderFailing expr = %q: ProviderFailingWindow was written for a range", expr)
		}
		for _, r := range ranges {
			if d, err := time.ParseDuration(r[1]); err != nil || d != ProviderFailingWindow {
				t.Errorf("CoddyProviderFailing range [%s], ProviderFailingWindow = %v", r[1], ProviderFailingWindow)
				break
			}
		}
	})
}

// Everything at once comes out in the order the document promises: the
// process-wide problems first, then the parts, then the providers, then the relays.
func TestAttentionOrder(t *testing.T) {
	s := Snapshot{
		RestartRequired: true,
		Disk:            &Disk{AvailableBytes: 0, SizeBytes: 1 << 40},
		Subsystems: []SubsystemStatus{
			{Kind: KindHTTP, State: StateRunning},
			{Kind: KindMetrics, State: StateStopped},
		},
		SwarmJoins: []swarm.JoinStatus{joinOffline("relay.example:12346", swarm.KindAgent, time.Hour)},
	}
	m := &MetricsBlock{Providers: []ProviderStats{{Name: "local-mlx", LastFailureAt: docNow.Add(-time.Minute)}}}
	want := []AttentionItem{
		{Code: AttentionRestartRequired},
		{Code: AttentionDiskLow},
		{Code: AttentionSubsystemDown, Subsystem: KindMetrics},
		{Code: AttentionProviderFailing, Provider: "local-mlx"},
		{Code: AttentionSwarmJoinDown, Relay: "relay.example:12346", OfflineSince: docNow.Add(-time.Hour)},
	}
	if got := attentionOf(s, m); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func fullSnapshot() Snapshot {
	return Snapshot{
		Build: Build{
			Version: "1.3.12", Revision: "050fbbbf", GoVersion: "go1.26.9",
			GOOS: "linux", GOARCH: "arm64", Tags: []string{"cli", "gateway", "http", "metrics"},
		},
		StartedAt: docNow.Add(-time.Hour), StartedBy: StartedBySystemd,
		ConfigFile: "/home/al/.coddy/config.yaml",
		Subsystems: []SubsystemStatus{
			{Kind: KindHTTP, State: StateRunning, Address: "127.0.0.1:12345", PendingAddress: "0.0.0.0:12345"},
			{Kind: KindGateway, State: StateStopped},
			{Kind: KindMetrics, State: StateRunning, Address: "127.0.0.1:12347", Credential: CredentialToken},
		},
		RestartRequired:     true,
		RestartRefused:      "gateways.telegram.enable is true but this binary has no gateway support (rebuild with -tags gateway)",
		TurnsRunning:        2,
		OldestTurnStartedAt: docNow.Add(-340*time.Second - 900*time.Millisecond),
		PermissionPrompts:   1,
		Questions:           2,
		Tasks:               map[string]map[string]int{"agent": {"running": 1}, "command": {"queued": 1}},
		Disk:                &Disk{AvailableBytes: 398458880, SizeBytes: 250000000000},
		SwarmJoins: []swarm.JoinStatus{
			{Relay: "relay.example:12346", Kind: swarm.KindAgent, Transport: swarm.TransportTunnel, OfflineSince: docNow.Add(-12 * time.Minute)},
			{Relay: "relay.example:12346", Kind: swarm.KindRelay, Transport: swarm.TransportDirect, OfflineSince: docNow.Add(-8 * time.Minute)},
		},
	}
}

func fullBlock() *MetricsBlock {
	return &MetricsBlock{
		Providers: []ProviderStats{
			{Name: "remote", Attempts: 3, Failures: 3, LastFailureAt: docNow.Add(-3 * time.Minute)},
			{
				Name: "local-mlx", Attempts: 1204, Failures: 4, MeanSeconds: new(6.1),
				LastFailureAt: docNow.Add(-2 * time.Minute), LastOKAt: docNow.Add(-15 * time.Second),
			},
		},
		Series: Series{
			StepSeconds: 15, End: docNow,
			AttemptsOK:         []float64{nan(), 3},
			ProviderErrors:     []float64{nan(), 0},
			AttemptSecondsMean: []float64{nan(), 5.8},
			TokensIn:           []float64{nan(), 38000},
			TokensOut:          []float64{nan(), 2100},
			TurnsRunning:       []float64{1, 2},
			TasksUnfinished:    []float64{3, 3},
		},
	}
}

func render(t *testing.T, s Snapshot, m *MetricsBlock, home string) string {
	t.Helper()
	out, err := json.Marshal(RenderStatus(s, m, docNow, home))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// The document of a full snapshot is the wire shape of GET /coddy/status:
// camelCase members in a fixed order, the home as ~, attention worked out
// here, NaN as null.
func TestRenderTheWholeDocument(t *testing.T) {
	want := strings.Join([]string{
		`{"object":"coddy.status","generatedAt":"2026-10-15T10:00:00Z",`,
		`"build":{"version":"1.3.12","revision":"050fbbbf","goVersion":"go1.26.9","os":"linux","arch":"arm64",`,
		`"tags":["cli","gateway","http","metrics"]},`,
		`"process":{"startedAt":"2026-10-15T09:00:00Z","startedBy":"systemd","configFile":"~/.coddy/config.yaml"},`,
		`"subsystems":[`,
		`{"kind":"httpserver","state":"running","address":"127.0.0.1:12345","pendingAddress":"0.0.0.0:12345"},`,
		`{"kind":"gateway","state":"stopped"},`,
		`{"kind":"metrics","state":"running","address":"127.0.0.1:12347","credential":"token"}],`,
		`"restartRequired":true,`,
		`"restartRefused":"gateways.telegram.enable is true but this binary has no gateway support (rebuild with -tags gateway)",`,
		`"turns":{"running":2,"oldestSeconds":340},`,
		`"prompts":{"permission":1,"question":2},`,
		`"tasks":{"agent":{"running":1},"command":{"queued":1}},`,
		`"disk":{"availableBytes":398458880,"sizeBytes":250000000000},`,
		`"attention":[{"code":"restart_required"},{"code":"disk_low"},`,
		`{"code":"subsystem_down","subsystem":"gateway"},{"code":"provider_failing","provider":"remote"},`,
		`{"code":"swarm_join_down","relay":"relay.example:12346","offlineSince":"2026-10-15T09:48:00Z"}],`,
		`"metrics":{"providers":[`,
		`{"name":"local-mlx","attempts":1204,"failures":4,"meanSeconds":6.1,`,
		`"lastFailureAt":"2026-10-15T09:58:00Z","lastOkAt":"2026-10-15T09:59:45Z"},`,
		`{"name":"remote","attempts":3,"failures":3,"lastFailureAt":"2026-10-15T09:57:00Z"}],`,
		`"series":{"stepSeconds":15,"end":"2026-10-15T10:00:00Z","attemptsOk":[null,3],"providerErrors":[null,0],`,
		`"attemptSecondsMean":[null,5.8],"tokensIn":[null,38000],"tokensOut":[null,2100],`,
		`"turnsRunning":[1,2],"tasksUnfinished":[3,3]}}}`,
	}, "")
	if got := render(t, fullSnapshot(), fullBlock(), "/home/al"); got != want {
		t.Errorf("document:\n got %s\nwant %s", got, want)
	}
}

// Optional members are left out, never sent as zero; only what the contract
// names stays, with the parts that cannot be empty.
func TestRenderLeavesOutWhatIsNotThere(t *testing.T) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(render(t, Snapshot{}, nil, "")), &doc); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]json.RawMessage) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	if want := []string{"build", "generatedAt", "object", "process", "subsystems", "turns"}; !reflect.DeepEqual(keys(doc), want) {
		t.Errorf("members of an empty snapshot = %v, want %v", keys(doc), want)
	}
	if got := string(doc["subsystems"]); got != "[]" {
		t.Errorf("subsystems = %s, want an empty array", got)
	}
	if got := string(doc["turns"]); got != `{"running":0}` {
		t.Errorf("turns = %s, want no oldestSeconds", got)
	}
	var build map[string]json.RawMessage
	if err := json.Unmarshal(doc["build"], &build); err != nil {
		t.Fatal(err)
	}
	if want := []string{"arch", "goVersion", "os", "version"}; !reflect.DeepEqual(keys(build), want) {
		t.Errorf("members of an empty build = %v, want %v", keys(build), want)
	}

	// The optional members come back when there is something to say.
	var full map[string]json.RawMessage
	if err := json.Unmarshal([]byte(render(t, Snapshot{Unavailable: []string{UnavailableDisk}}, fullBlock(), "")), &full); err != nil {
		t.Fatal(err)
	}
	if string(full["unavailable"]) != `["disk"]` {
		t.Errorf("unavailable = %s", full["unavailable"])
	}
	if _, ok := full["metrics"]; !ok {
		t.Error("a block was given and the document has no metrics")
	}
}

// The prompts that wait for a person are one member, left out when none does
// and without a count that is zero.
func TestRenderPrompts(t *testing.T) {
	for _, tc := range []struct {
		name               string
		permission, asking int
		want               string
	}{
		{"none", 0, 0, ""},
		{"a permission", 1, 0, `"prompts":{"permission":1}`},
		{"a question", 0, 3, `"prompts":{"question":3}`},
		{"both", 2, 1, `"prompts":{"permission":2,"question":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := render(t, Snapshot{PermissionPrompts: tc.permission, Questions: tc.asking}, nil, "")
			if tc.want == "" {
				if strings.Contains(out, `"prompts"`) {
					t.Errorf("document has prompts: %s", out)
				}
				return
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("document %s lacks %s", out, tc.want)
			}
		})
	}
}

func TestRenderTasks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tasks map[string]map[string]int
		want  string
	}{
		{"nil", nil, ""},
		{"empty", map[string]map[string]int{}, ""},
		{"a kind with nothing in it", map[string]map[string]int{"agent": {}}, ""},
		{"counts of zero", map[string]map[string]int{"agent": {"running": 0}, "command": {"queued": 0}}, ""},
		{"zeros beside real counts", map[string]map[string]int{"agent": {"running": 2, "queued": 0}, "command": {}}, `"tasks":{"agent":{"running":2}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := render(t, Snapshot{Tasks: tc.tasks}, nil, "")
			if tc.want == "" {
				if strings.Contains(out, `"tasks"`) {
					t.Errorf("document has tasks: %s", out)
				}
				return
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("document %s lacks %s", out, tc.want)
			}
		})
	}
}

func TestRenderTurns(t *testing.T) {
	for _, tc := range []struct {
		name    string
		running int
		oldest  time.Time
		want    string
	}{
		{"none", 0, time.Time{}, `"turns":{"running":0}`},
		{"one, whole seconds", 1, docNow.Add(-90*time.Second - 999*time.Millisecond), `"turns":{"running":1,"oldestSeconds":90}`},
		{"one that began this second", 1, docNow, `"turns":{"running":1,"oldestSeconds":0}`},
		{"a start in the future is not negative", 1, docNow.Add(time.Minute), `"turns":{"running":1,"oldestSeconds":0}`},
		{"a turn that ended between the reads", 0, docNow.Add(-time.Minute), `"turns":{"running":0}`},
		{"a turn whose start was not read", 1, time.Time{}, `"turns":{"running":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := render(t, Snapshot{TurnsRunning: tc.running, OldestTurnStartedAt: tc.oldest}, nil, "")
			if !strings.Contains(out, tc.want) {
				t.Errorf("document %s lacks %s", out, tc.want)
			}
		})
	}
}

// A disk with no room left is a figure of 0, not an absent one.
func TestRenderDiskFull(t *testing.T) {
	out := render(t, Snapshot{Disk: &Disk{AvailableBytes: 0, SizeBytes: 1 << 30}}, nil, "")
	if !strings.Contains(out, `"disk":{"availableBytes":0,"sizeBytes":1073741824}`) ||
		!strings.Contains(out, `"attention":[{"code":"disk_low"}]`) {
		t.Errorf("document = %s", out)
	}
}

func TestRenderTimesAreUTC(t *testing.T) {
	zone := time.FixedZone("CEST", 2*3600)
	s := Snapshot{StartedAt: time.Date(2026, 10, 15, 11, 0, 0, 0, zone)}
	m := &MetricsBlock{Series: Series{StepSeconds: 1, End: time.Date(2026, 10, 15, 12, 0, 0, 0, zone)}}
	out, err := json.Marshal(RenderStatus(s, m, time.Date(2026, 10, 15, 12, 0, 0, 0, zone), ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"generatedAt":"2026-10-15T10:00:00Z"`, `"startedAt":"2026-10-15T09:00:00Z"`, `"end":"2026-10-15T10:00:00Z"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("document %s lacks %s", out, want)
		}
	}
}

func TestRenderMetrics(t *testing.T) {
	t.Run("a binary without the exporter has no block and no failing provider", func(t *testing.T) {
		out := render(t, fullSnapshot(), nil, "/home/al")
		if strings.Contains(out, `"metrics":`) || strings.Contains(out, "provider_failing") {
			t.Errorf("document = %s", out)
		}
	})

	t.Run("an exporter with nothing yet still has the block", func(t *testing.T) {
		out := render(t, Snapshot{}, &MetricsBlock{}, "")
		if !strings.Contains(out, `"metrics":{"providers":[],"series":{"stepSeconds":0,`) ||
			!strings.Contains(out, `"attemptsOk":[],`) || !strings.Contains(out, `"tasksUnfinished":[]}}`) {
			t.Errorf("document = %s", out)
		}
	})

	t.Run("the mean is the exporter's, and left out when it has none", func(t *testing.T) {
		m := &MetricsBlock{Providers: []ProviderStats{
			{Name: "a", MeanSeconds: new(2.5)},
			{Name: "b"},
			{Name: "c", MeanSeconds: new(0.0)},
		}}
		out := render(t, Snapshot{}, m, "")
		for _, want := range []string{
			`{"name":"a","attempts":0,"failures":0,"meanSeconds":2.5}`,
			`{"name":"b","attempts":0,"failures":0}`,
			`{"name":"c","attempts":0,"failures":0,"meanSeconds":0}`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("document %s lacks %s", out, want)
			}
		}
	})

	t.Run("the providers are listed by name and the block is left alone", func(t *testing.T) {
		m := &MetricsBlock{Providers: []ProviderStats{{Name: "b"}, {Name: "a"}}}
		out := render(t, Snapshot{}, m, "")
		if strings.Index(out, `"name":"a"`) > strings.Index(out, `"name":"b"`) {
			t.Errorf("providers out of order: %s", out)
		}
		if m.Providers[0].Name != "b" {
			t.Error("RenderStatus reordered the block's slice")
		}
	})
}

func TestSamplesGapsAreNull(t *testing.T) {
	in := Samples{nan(), 1.5, math.Inf(1), 0, math.Inf(-1)}
	out, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `[null,1.5,null,0,null]` {
		t.Errorf("marshalled %s", out)
	}
	if out, _ := json.Marshal(Samples(nil)); string(out) != `[]` {
		t.Errorf("nil marshalled %s, want an empty array", out)
	}

	// What a client reads back: null is a gap, and nothing else is.
	var back []*float64
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if len(back) != len(in) || back[0] != nil || *back[1] != 1.5 || back[2] != nil || *back[3] != 0 || back[4] != nil {
		t.Errorf("read back %v from %s, want a gap, 1.5, a gap, 0, a gap", back, out)
	}
}

func TestTildeHome(t *testing.T) {
	for _, tc := range []struct {
		name, path, home, want string
		fold, backslash        bool
	}{
		{name: "the home itself", path: "/home/al", home: "/home/al", want: "~"},
		{name: "a file under it", path: "/home/al/.coddy/config.yaml", home: "/home/al", want: "~/.coddy/config.yaml"},
		{name: "a home written with a trailing separator", path: "/home/al/.coddy/config.yaml", home: "/home/al/", want: "~/.coddy/config.yaml"},
		{name: "a sibling that shares the prefix", path: "/home/alice/.coddy/config.yaml", home: "/home/al", want: "/home/alice/.coddy/config.yaml"},
		{name: "a name that starts with the home", path: "/home/al.bak", home: "/home/al", want: "/home/al.bak"},
		{name: "a folder elsewhere", path: "/srv/coddy/config.yaml", home: "/home/al", want: "/srv/coddy/config.yaml"},
		{name: "a parent of the home", path: "/home", home: "/home/al", want: "/home"},
		{name: "a relative path", path: "config.yaml", home: "/home/al", want: "config.yaml"},
		{name: "an empty path", path: "", home: "/home/al", want: ""},
		{name: "no home", path: "/home/al/x", home: "", want: "/home/al/x"},
		{name: "the root as a home", path: "/etc/x", home: "/", want: "/etc/x"},
		{name: "case matters here", path: "/HOME/AL/x", home: "/home/al", want: "/HOME/AL/x"},
		{name: "case does not matter there", path: "/HOME/AL/x", home: "/home/al", want: "~/x", fold: true},
		{name: "a sibling is still a sibling there", path: "/HOME/ALICE/x", home: "/home/al", want: "/HOME/ALICE/x", fold: true},
		{name: "a windows path", path: `C:\Users\al\.coddy\config.yaml`, home: `C:\Users\al`, want: `~\.coddy\config.yaml`, fold: true, backslash: true},
		{name: "a windows path in another case", path: `c:\users\AL\config.yaml`, home: `C:\Users\al`, want: `~\config.yaml`, fold: true, backslash: true},
		{name: "a windows path with forward slashes", path: `C:\Users\al/.coddy/config.yaml`, home: `C:\Users\al`, want: `~/.coddy/config.yaml`, fold: true, backslash: true},
		{name: "a windows sibling", path: `C:\Users\alice\config.yaml`, home: `C:\Users\al`, want: `C:\Users\alice\config.yaml`, fold: true, backslash: true},
		{name: "a drive as a home", path: `C:\Users\al\config.yaml`, home: `C:\`, want: `C:\Users\al\config.yaml`, fold: true, backslash: true},
		{name: "a backslash is a name on other systems", path: `/home/al\x`, home: "/home/al", want: `/home/al\x`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tilde(tc.path, tc.home, tc.fold, tc.backslash); got != tc.want {
				t.Errorf("tilde(%q, %q) = %q, want %q", tc.path, tc.home, got, tc.want)
			}
		})
	}

	t.Run("this platform", func(t *testing.T) {
		home := filepath.FromSlash("/home/al")
		path := filepath.FromSlash("/HOME/AL/.coddy/config.yaml")
		want := path
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			want = "~" + filepath.FromSlash("/.coddy/config.yaml")
		}
		if got := tildeHome(path, home); got != want {
			t.Errorf("tildeHome(%q, %q) = %q, want %q", path, home, got, want)
		}
	})
}

// Only the process's own configuration file is a path in the document, and
// the home it sits under is the operating system's.
func TestRenderShowsTheHomeAsATilde(t *testing.T) {
	home := filepath.FromSlash("/home/al")
	s := Snapshot{ConfigFile: filepath.Join(home, ".coddy", "config.yaml")}
	out := render(t, s, nil, home)
	want, _ := json.Marshal("~" + string(filepath.Separator) + filepath.Join(".coddy", "config.yaml"))
	if !strings.Contains(out, `"configFile":`+string(want)) || strings.Contains(out, "/home/al") {
		t.Errorf("document = %s", out)
	}
	s.ConfigFile = filepath.FromSlash("/srv/coddy/config.yaml")
	outside, _ := json.Marshal(s.ConfigFile)
	if out := render(t, s, nil, home); !strings.Contains(out, `"configFile":`+string(outside)) {
		t.Errorf("a file outside the home changed: %s", out)
	}
}

// The status document is the snapshot of the process rendered for the API:
// the configuration file with the operating system's home written as ~, no
// metrics member while no exporter set a source, and the exporter's figures,
// and the provider it sees failing, once one did.
func TestRuntimeStatusDocument(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	coddyHome := filepath.Join(home, ".coddy")
	rt := &Runtime{}
	if err := rt.Init(Options{
		Cfg: &config.Config{},
		CLI: config.CLIPaths{Home: coddyHome, CWD: t.TempDir(), Config: filepath.Join(coddyHome, "config.yaml")},
	}); err != nil {
		t.Fatal(err)
	}

	t.Run("without a source the document has no metrics", func(t *testing.T) {
		before := time.Now()
		doc := rt.StatusDocument()
		if doc.Object != StatusObject || doc.Build.Version != version.Get() {
			t.Errorf("document = %+v", doc)
		}
		if doc.GeneratedAt.Before(before.UTC().Add(-time.Second)) || doc.GeneratedAt.After(time.Now().UTC().Add(time.Second)) {
			t.Errorf("generatedAt = %v, want about now", doc.GeneratedAt)
		}
		if want := "~" + string(filepath.Separator) + filepath.Join(".coddy", "config.yaml"); doc.Process.ConfigFile != want {
			t.Errorf("configFile = %q, want %q: the home of the operating system is a tilde", doc.Process.ConfigFile, want)
		}
		if doc.Process.StartedBy != StartedByForeground || doc.Process.StartedAt.IsZero() {
			t.Errorf("process = %+v, want a foreground start with a time", doc.Process)
		}
		if doc.Metrics != nil {
			t.Errorf("metrics = %+v, want none", doc.Metrics)
		}
	})

	t.Run("a source adds the metrics block and the failing provider", func(t *testing.T) {
		failed := time.Now().Add(-time.Minute)
		rt.SetMetricsSource(func() *MetricsBlock {
			return &MetricsBlock{
				Providers: []ProviderStats{{Name: "local-mlx", Attempts: 12, Failures: 3, MeanSeconds: new(2.0), LastFailureAt: failed}},
				Series:    Series{StepSeconds: 15, End: failed, AttemptsOK: []float64{math.NaN(), 3}},
			}
		})
		doc := rt.StatusDocument()
		if doc.Metrics == nil || len(doc.Metrics.Providers) != 1 || doc.Metrics.Providers[0].Name != "local-mlx" {
			t.Fatalf("metrics = %+v, want the source's one provider", doc.Metrics)
		}
		if got := doc.Metrics.Providers[0]; got.Attempts != 12 || got.Failures != 3 || got.MeanSeconds == nil || *got.MeanSeconds != 2 {
			t.Errorf("provider = %+v", got)
		}
		if got := doc.Metrics.Series.AttemptsOK; len(got) != 2 || got[1] != 3 {
			t.Errorf("attemptsOk = %v", got)
		}
		want := []AttentionItem{{Code: AttentionProviderFailing, Provider: "local-mlx"}}
		if !reflect.DeepEqual(doc.Attention, want) {
			t.Errorf("attention = %+v, want %+v", doc.Attention, want)
		}
	})

	t.Run("a source with nothing to say leaves the block out", func(t *testing.T) {
		rt.SetMetricsSource(func() *MetricsBlock { return nil })
		if doc := rt.StatusDocument(); doc.Metrics != nil {
			t.Errorf("metrics = %+v from a source that has none", doc.Metrics)
		}
	})

	t.Run("clearing the source takes the block away again", func(t *testing.T) {
		rt.SetMetricsSource(func() *MetricsBlock { return &MetricsBlock{} })
		rt.SetMetricsSource(nil)
		if doc := rt.StatusDocument(); doc.Metrics != nil {
			t.Errorf("metrics = %+v after the source was cleared", doc.Metrics)
		}
	})

	t.Run("a home that cannot be read leaves the path as it is", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows finds the home of an account without USERPROFILE")
		}
		t.Setenv("HOME", "")
		if got := rt.StatusDocument().Process.ConfigFile; got != rt.Paths.ConfigPath {
			t.Errorf("configFile = %q, want %q", got, rt.Paths.ConfigPath)
		}
	})
}

// The source is a slot an exporter fills and empties while the API reads it.
func TestRuntimeMetricsSourceIsSafeToSwapWhileReading(t *testing.T) {
	rt := &Runtime{}
	done := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_ = rt.StatusDocument()
				}
			}
		}()
	}
	for range 200 {
		rt.SetMetricsSource(func() *MetricsBlock { return &MetricsBlock{} })
		rt.SetMetricsSource(nil)
	}
	close(done)
	wg.Wait()
}
