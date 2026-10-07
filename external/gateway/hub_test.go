//go:build gateway || gateway.telegram || gateway.pachca

package gateway

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// slowStop is an adapter that needs a moment to finish after its context ends,
// like a bot draining the turns it already took.
type slowStop struct{ finished atomic.Bool }

func (a *slowStop) Name() string { return "slow" }

func (a *slowStop) Start(ctx context.Context) error {
	<-ctx.Done()
	time.Sleep(100 * time.Millisecond)
	a.finished.Store(true)
	return nil
}

// TestHubWaitsForItsAdapters holds Start to its promise: when it returns, no
// adapter is still running, so a rebuilt bot never overlaps the old one.
func TestHubWaitsForItsAdapters(t *testing.T) {
	a := &slowStop{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		NewHub(slog.New(slog.DiscardHandler), a).Start(ctx)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	if !a.finished.Load() {
		t.Fatal("Hub.Start returned while an adapter was still stopping")
	}
}
