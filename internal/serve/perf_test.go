package serve

// The performance group: benchmarks only, run by `make test-perf` and never by
// `make test`. What one GET /coddy/status costs once the snapshot is read: the
// document of a process with every subsystem, waiting prompts, background
// tasks, a relay down and the widest metrics block, and the JSON the handler
// writes, reported next to the timing as its size.

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// wideMetrics is the block of a process at the exporter's label budget: 16
// providers and an hour of seven series, a gap in every tenth point.
func wideMetrics(end time.Time) *MetricsBlock {
	providers := make([]ProviderStats, 16)
	for i := range providers {
		mean := 1.8
		providers[i] = ProviderStats{
			Name: fmt.Sprintf("provider-%02d", i), Attempts: 4000, Failures: 12, MeanSeconds: &mean,
			LastFailureAt: end.Add(-20 * time.Minute), LastOKAt: end.Add(-time.Second),
		}
	}
	series := func(base float64) Samples {
		out := make(Samples, 240)
		for i := range out {
			out[i] = base + float64(i%17)
			if i%10 == 0 {
				out[i] = math.NaN()
			}
		}
		return out
	}
	return &MetricsBlock{Providers: providers, Series: Series{
		StepSeconds: 15, End: end,
		AttemptsOK: series(12), ProviderErrors: series(0), AttemptSecondsMean: series(1.5),
		TokensIn: series(4000), TokensOut: series(900), TurnsRunning: series(1), TasksUnfinished: series(0),
	}}
}

func BenchmarkStatusDocument(b *testing.B) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	snap := Snapshot{
		Build:     Build{Version: "1.3.19", GoVersion: "go1.26.9", GOOS: "linux", GOARCH: "amd64", Tags: []string{"http", "ui", "metrics"}},
		StartedAt: now.Add(-3 * time.Hour), StartedBy: StartedByForeground, ConfigFile: "/home/user/.coddy/config.yaml",
		Subsystems: []SubsystemStatus{
			{Kind: KindHTTP, State: StateRunning, Address: "127.0.0.1:12345", PendingAddress: "0.0.0.0:12345"},
			{Kind: KindGateway, State: StateRunning}, {Kind: KindSwarm, State: StateRunning, Address: "0.0.0.0:12346"},
			{Kind: KindScheduler, State: StateRunning}, {Kind: KindMetrics, State: StateStopped, Credential: CredentialToken},
		},
		RestartRequired: true, TurnsRunning: 3, OldestTurnStartedAt: now.Add(-90 * time.Second),
		Tasks:             map[string]map[string]int{"command": {"running": 2, "queued": 1}, "agent": {"running": 1}},
		Disk:              &Disk{AvailableBytes: 120 << 30, SizeBytes: 500 << 30},
		PermissionPrompts: 1, Questions: 1,
		SwarmJoins: []swarm.JoinStatus{{Relay: "relay.example.org:12346", Kind: "agent", Transport: "tunnel", OfflineSince: now.Add(-time.Hour)}},
	}
	block := wideMetrics(now)
	var size int
	b.ReportAllocs()
	for b.Loop() {
		out, err := json.Marshal(RenderStatus(snap, block, now, "/home/user"))
		if err != nil {
			b.Fatal(err)
		}
		size = len(out)
	}
	b.ReportMetric(float64(size), "bytes/doc")
}
