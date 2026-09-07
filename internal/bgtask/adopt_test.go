package bgtask

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// countingHandle records how often the pool waits on adopted work: exactly one
// cmd.Wait exists for a command the shell tool started, so the pool must
// observe it, never call it a second time.
type countingHandle struct {
	stubHandle
	mu    sync.Mutex
	waits int
}

func (h *countingHandle) Wait() (int, error) {
	h.mu.Lock()
	h.waits++
	h.mu.Unlock()
	return h.stubHandle.Wait()
}

func (h *countingHandle) waitCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.waits
}

func newCountingHandle() *countingHandle {
	return &countingHandle{stubHandle: stubHandle{release: make(chan struct{})}}
}

func TestAdoptRegistersAlreadyRunningWork(t *testing.T) {
	p := newTestPool(t, &stubRunner{}, Config{})
	h := newCountingHandle()

	snap, err := p.Adopt(Spec{SessionID: "s1", Kind: KindCommand, Command: "npm run serve"}, func(out io.Writer) (Handle, error) {
		// Adoption flushes what the foreground run already captured, so the
		// background log reads as one stream.
		_, _ = io.WriteString(out, "App running at http://localhost:8081/\n")
		return h, nil
	})
	if err != nil {
		t.Fatalf("Adopt(): %v", err)
	}
	if snap.Status != StatusRunning {
		t.Fatalf("adopted task is %q, want running", snap.Status)
	}

	text, _, err := p.Output("s1", snap.ID, 0)
	if err != nil {
		t.Fatalf("Output(): %v", err)
	}
	if !strings.Contains(text, "App running at http://localhost:8081/") {
		t.Fatalf("adopted task output %q lost the prefix flushed at handover", text)
	}

	h.finish(0)
	waitForStatus(t, p, "s1", snap.ID, StatusSucceeded)
	if got := h.waitCount(); got != 1 {
		t.Fatalf("the pool waited on the adopted handle %d times, want exactly 1", got)
	}
}

func TestAdoptGivesTheTaskThePoolMaximumTimeout(t *testing.T) {
	p := newTestPool(t, &stubRunner{}, Config{MaxTimeoutSeconds: 1800})
	h := newCountingHandle()
	t.Cleanup(func() { h.finish(0) })

	// A zero timeout must not fall back to the ordinary default, and must
	// certainly not reuse the foreground limit that just expired.
	snap, err := p.Adopt(Spec{SessionID: "s1", Command: "sleep 600"}, func(io.Writer) (Handle, error) { return h, nil })
	if err != nil {
		t.Fatalf("Adopt(): %v", err)
	}
	if snap.TimeoutSeconds != 1800 {
		t.Fatalf("adopted task hard timeout = %ds, want the pool maximum 1800s", snap.TimeoutSeconds)
	}

	explicit := newCountingHandle()
	t.Cleanup(func() { explicit.finish(0) })
	other, err := p.Adopt(Spec{SessionID: "s1", Command: "sleep 600", TimeoutSeconds: 120}, func(io.Writer) (Handle, error) { return explicit, nil })
	if err != nil {
		t.Fatalf("Adopt(): %v", err)
	}
	if other.TimeoutSeconds != 120 {
		t.Fatalf("an explicit adopted timeout became %ds, want 120s", other.TimeoutSeconds)
	}
}

func TestAdoptKeepsTheOriginalStartTime(t *testing.T) {
	p := newTestPool(t, &stubRunner{}, Config{})
	h := newCountingHandle()
	t.Cleanup(func() { h.finish(0) })

	started := time.Now().Add(-time.Hour)
	snap, err := p.Adopt(Spec{SessionID: "s1", Command: "sleep 600", StartedAt: started}, func(io.Writer) (Handle, error) { return h, nil })
	if err != nil {
		t.Fatalf("Adopt(): %v", err)
	}
	if elapsed := snap.Elapsed(time.Now()); elapsed < time.Hour {
		t.Fatalf("adopted task reports %s elapsed, want at least the hour it already ran", elapsed)
	}
}

func TestAdoptRefusesWhenTheSessionIsFull(t *testing.T) {
	runner := &stubRunner{}
	p := newTestPool(t, runner, Config{MaxConcurrent: 1})
	if _, err := p.Start(Spec{SessionID: "s1", Command: "first"}); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	t.Cleanup(func() { runner.last().finish(0) })

	called := false
	_, err := p.Adopt(Spec{SessionID: "s1", Command: "second"}, func(io.Writer) (Handle, error) {
		called = true
		return newCountingHandle(), nil
	})
	if !errors.Is(err, ErrPoolFull) {
		t.Fatalf("Adopt() error = %v, want ErrPoolFull", err)
	}
	// The caller still owns the process and has to terminate it, so it must be
	// able to trust that nothing was handed over.
	if called {
		t.Fatal("Adopt ran the handover callback after refusing the task")
	}
}

func TestAdoptRefusesWhileDraining(t *testing.T) {
	p := newTestPool(t, &stubRunner{}, Config{})
	p.SetDraining(true)

	called := false
	_, err := p.Adopt(Spec{SessionID: "s1", Command: "late"}, func(io.Writer) (Handle, error) {
		called = true
		return newCountingHandle(), nil
	})
	if !errors.Is(err, ErrDraining) {
		t.Fatalf("Adopt() error = %v, want ErrDraining", err)
	}
	if called {
		t.Fatal("Adopt ran the handover callback while draining")
	}
}

func TestAdoptRejectsANilCallback(t *testing.T) {
	p := newTestPool(t, &stubRunner{}, Config{})
	if _, err := p.Adopt(Spec{SessionID: "s1", Command: "x"}, nil); err == nil {
		t.Fatal("Adopt(nil) should be refused")
	}
}

func TestStopReachesAnAdoptedTask(t *testing.T) {
	p := newTestPool(t, &stubRunner{}, Config{})
	h := newCountingHandle()

	snap, err := p.Adopt(Spec{SessionID: "s1", Command: "npm run serve"}, func(io.Writer) (Handle, error) { return h, nil })
	if err != nil {
		t.Fatalf("Adopt(): %v", err)
	}

	stopped, err := p.Stop("s1", snap.ID)
	if err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	if stopped.Status != StatusStopped {
		t.Fatalf("adopted task is %q after Stop, want stopped", stopped.Status)
	}
}
