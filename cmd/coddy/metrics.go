//go:build metrics

package main

import (
	"context"
	"errors"

	"github.com/EvilFreelancer/coddy-agent/external/metrics"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

// exporter runs the Prometheus exporter of external/metrics as a subsystem of
// coddy serve. The collector is built once, by Start, and outlives every
// listener Run opens, so a rebuild on another address keeps the counters.
type exporter struct {
	rt        *serve.Runtime
	collector *metrics.Collector
}

func newMetricsWiring(rt *serve.Runtime) metricsWiring { return &exporter{rt: rt} }

func (e *exporter) Start() (stop func()) {
	e.collector = metrics.NewCollector(metrics.Source{
		Status:            e.rt.Status,
		Cfg:               e.rt.Cfg,
		Mgr:               e.rt.Mgr,
		AddConfigObserver: e.rt.AddConfigObserver,
		Log:               e.rt.Log,
	})
	return e.collector.Close
}

func (e *exporter) Subsystem() serve.Subsystem {
	sub := metricsDescriptor()
	sub.Available = true
	// Only the address rebuilds the listener; the token is read on every request.
	// There is no RestartKey: nobody configures Coddy through /metrics.
	sub.Fingerprint = func(c *config.Config) string { return c.Metrics.ListenAddr() }
	sub.Run = func(ctx context.Context, cfg *config.Config) error {
		if e.collector == nil {
			return errors.New("metrics: the exporter was not started")
		}
		return metrics.Serve(ctx, metrics.Options{
			Addr: cfg.Metrics.ListenAddr(), Cfg: e.rt.Cfg, Log: e.rt.Log, Collector: e.collector,
		})
	}
	return sub
}
