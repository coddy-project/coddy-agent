package serve

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestSupervisorRunWaitsForSubsystemReadiness(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	allowReady := make(chan struct{})
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup := NewSupervisor(nil, []Subsystem{{
		Kind: KindHTTP, Available: true, Enabled: func(*config.Config) bool { return true }, ManualReady: true,
		Run: func(ctx context.Context, _ *config.Config) error {
			close(started)
			<-allowReady
			ReportReady(ctx)
			<-ctx.Done()
			return nil
		},
	}})
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, &config.Config{}, nil, func() { close(ready) }) }()
	<-started
	select {
	case <-ready:
		t.Fatal("supervisor reported readiness before the subsystem")
	default:
	}
	close(allowReady)
	waitForSignal(t, ready)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorRunDoesNotReportReadinessAfterStartupFailure(t *testing.T) {
	t.Parallel()
	sup := NewSupervisor(nil, []Subsystem{{
		Kind: KindHTTP, Available: true, Enabled: func(*config.Config) bool { return true }, ManualReady: true,
		Run: func(context.Context, *config.Config) error { return errors.New("bind failed") },
	}})
	err := sup.Run(context.Background(), &config.Config{}, nil, func() {
		t.Error("reported readiness after startup failure")
	})
	if err == nil || !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "bind failed") {
		t.Fatalf("Run error = %v", err)
	}
}

func waitForSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for signal")
	}
}
