//go:build !metrics

package main

import "github.com/EvilFreelancer/coddy-agent/internal/serve"

// A binary built without the metrics tag still describes the exporter, so a
// configuration that enables it is refused by name instead of being left to
// do nothing.
type noExporter struct{}

func newMetricsWiring(*serve.Runtime) metricsWiring { return noExporter{} }

func (noExporter) Subsystem() serve.Subsystem { return metricsDescriptor() }

func (noExporter) Start() (stop func()) { return func() {} }
