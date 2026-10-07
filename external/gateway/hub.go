//go:build gateway || gateway.telegram || gateway.pachca

package gateway

import (
	"context"
	"log/slog"
	"time"
)

// Hub manages multiple Adapter instances and restarts them on failure.
type Hub struct {
	adapters []Adapter
	log      *slog.Logger
}

// NewHub creates a hub with the given adapters.
func NewHub(log *slog.Logger, adapters ...Adapter) *Hub {
	return &Hub{adapters: adapters, log: log}
}

// Start launches all adapters in parallel goroutines.
// Each adapter is automatically restarted after a 5-second backoff on error.
// Blocks until ctx is cancelled and every adapter has returned, so a bot that
// is rebuilt never overlaps the one it replaces.
func (h *Hub) Start(ctx context.Context) {
	done := make(chan struct{}, len(h.adapters))
	for _, a := range h.adapters {
		a := a
		go func() {
			defer func() { done <- struct{}{} }()
			for {
				h.log.Info("gateway adapter starting", "name", a.Name())
				err := a.Start(ctx)
				if ctx.Err() != nil {
					// Context cancelled — clean shutdown.
					h.log.Info("gateway adapter stopped", "name", a.Name())
					return
				}
				if err != nil {
					h.log.Warn("gateway adapter error, restarting", "name", a.Name(), "err", err, "backoff", "5s")
					select {
					case <-ctx.Done():
						return
					case <-time.After(5 * time.Second):
					}
				}
			}
		}()
	}
	<-ctx.Done()
	for range h.adapters {
		<-done
	}
}
