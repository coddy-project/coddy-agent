//go:build metrics

package metrics

// The performance group of the exporter: benchmarks only, run by
// `make test-perf` and never by `make test`. They measure what the System page
// adds to a process built with the metrics tag: one tick of the sampler, which
// every 15 seconds writes the sampled families the way a scrape does and parses
// them back, and the block GET /coddy/status reads off the ring. The tick also
// reports its share of one core at one tick per sampleEvery.

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// budgetCollector is a collector at the widest the label budget lets it grow:
// 16 providers of 4 models each, every pair with answered and failed attempts,
// so every sampled family carries every series it can.
func budgetCollector(b *testing.B) *Collector {
	b.Helper()
	models := map[string][]string{}
	for p := 0; p < 16; p++ {
		models[fmt.Sprintf("p%02d", p)] = []string{"m0", "m1", "m2", "m3"}
	}
	l := newLive(configWith(models))
	c := newCollectorAt(l.source(), t0, idle)
	b.Cleanup(c.Close)
	for name, ms := range models {
		for _, m := range ms {
			attempt(name, m, answered(1200, 300, 400), nil)
			attempt(name, m, nil, errors.New("upstream answered 500"))
		}
	}
	return c
}

func BenchmarkSamplerTick(b *testing.B) {
	c := budgetCollector(b)
	now := t0
	b.ReportAllocs()
	start := time.Now()
	n := 0
	for b.Loop() {
		now = now.Add(sampleEvery)
		c.tick(now)
		n++
	}
	perTick := time.Since(start) / time.Duration(n)
	b.ReportMetric(100*perTick.Seconds()/sampleEvery.Seconds(), "%core")
}

func BenchmarkMetricsBlock(b *testing.B) {
	c := budgetCollector(b)
	now := t0
	for range samplePoints {
		now = now.Add(sampleEvery)
		c.tick(now)
	}
	b.ReportAllocs()
	for b.Loop() {
		if c.Metrics() == nil {
			b.Fatal("no block")
		}
	}
}
