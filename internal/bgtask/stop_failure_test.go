package bgtask

import (
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"
)

var errTestStop = errors.New("stop failed")

type failingStopHandle struct {
	*stubHandle
	entered     chan struct{}
	releaseStop chan struct{}
}

func (h *failingStopHandle) Stop(time.Duration) error {
	close(h.entered)
	<-h.releaseStop
	return errTestStop
}

type unsettledStopHandle struct{ *stubHandle }

func (h *unsettledStopHandle) Stop(time.Duration) error { return nil }

func TestStopWithTimeoutDoesNotClaimSettlement(t *testing.T) {
	p := NewWithRunner(Config{}, &stubRunner{})
	h := &unsettledStopHandle{&stubHandle{release: make(chan struct{})}}
	t.Cleanup(func() { h.finish(0) })
	snap, err := p.Launch(Spec{SessionID: "s1", Kind: KindCommand, Command: "fixture"}, func(string, io.Writer) (Handle, error) { return h, nil })
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		got, err := p.StopWithTimeout("s1", snap.ID, 20*time.Millisecond)
		if !errors.Is(err, ErrStopNotSettled) || got.Status.Finished() {
			t.Fatalf("stop = %+v, %v; want unsettled task", got, err)
		}
	}
}

func launchStopFixture(t *testing.T, p *Pool, h Handle, timeout int) Snapshot {
	t.Helper()
	snap, err := p.Launch(Spec{SessionID: "s1", Kind: KindCommand, Command: "fixture", TimeoutSeconds: timeout}, func(string, io.Writer) (Handle, error) { return h, nil })
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func stopAsync(p *Pool, id string, timeout time.Duration) <-chan error {
	result := make(chan error, 1)
	go func() {
		_, err := p.StopWithTimeout("s1", id, timeout)
		result <- err
	}()
	return result
}

func TestStopWithTimeoutBoundsFirstAndConcurrentCallers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := NewWithRunner(Config{}, &stubRunner{})
		h := &failingStopHandle{stubHandle: &stubHandle{release: make(chan struct{})}, entered: make(chan struct{}), releaseStop: make(chan struct{})}
		defer h.finish(0)
		released := false
		defer func() {
			if !released {
				close(h.releaseStop)
			}
		}()
		snap := launchStopFixture(t, p, h, 0)
		first := stopAsync(p, snap.ID, time.Second)
		<-h.entered
		concurrent := stopAsync(p, snap.ID, time.Second)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		for _, result := range []<-chan error{first, concurrent} {
			select {
			case err := <-result:
				if !errors.Is(err, ErrStopNotSettled) {
					t.Errorf("stop error = %v, want timeout", err)
				}
			default:
				t.Error("caller still blocked in Stop")
			}
		}
		// A retry while Stop is still blocked must join it, not call the handle a
		// second time (the handle would panic on closing entered twice).
		retry := stopAsync(p, snap.ID, time.Second)
		synctest.Wait()
		close(h.releaseStop)
		if err := <-retry; !errors.Is(err, errTestStop) {
			t.Errorf("retry error = %v, want stop error", err)
		}
		released = true
	})
}

type orderedStopHandle struct {
	*stubHandle
	stop   func() error
	reaped chan struct{}
}

func (h *orderedStopHandle) Stop(time.Duration) error { return h.stop() }
func (h *orderedStopHandle) Wait() (int, error) {
	code, err := h.stubHandle.Wait()
	close(h.reaped)
	return code, err
}

func TestConcurrentStopReceivesFailedTerminationAndCanRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := NewWithRunner(Config{}, &stubRunner{})
		h := &orderedStopHandle{stubHandle: &stubHandle{release: make(chan struct{})}, reaped: make(chan struct{})}
		defer h.finish(0)
		entered, fail := make(chan struct{}), make(chan struct{})
		calls := 0
		h.stop = func() error {
			calls++
			if calls == 1 {
				close(entered)
				<-fail
				return errTestStop
			}
			h.finish(143)
			return nil
		}
		snap := launchStopFixture(t, p, h, 0)
		first := stopAsync(p, snap.ID, time.Second)
		<-entered
		concurrent := stopAsync(p, snap.ID, time.Second)
		synctest.Wait()
		close(fail)
		for _, result := range []<-chan error{first, concurrent} {
			if err := <-result; !errors.Is(err, errTestStop) {
				t.Errorf("stop error = %v, want stop error", err)
			}
		}
		settled, err := p.StopWithTimeout("s1", snap.ID, time.Second)
		if err != nil || settled.Status != StatusStopped || calls != 2 {
			t.Fatalf("retry: status=%s error=%v calls=%d", settled.Status, err, calls)
		}
	})
}

func TestStopResultDeterminesStatusAfterExit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stopErr error
		want    Status
	}{
		{"failed", errTestStop, StatusSucceeded},
		{"succeeded", nil, StatusStopped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := NewWithRunner(Config{}, &stubRunner{})
				h := &orderedStopHandle{stubHandle: &stubHandle{release: make(chan struct{})}, reaped: make(chan struct{})}
				entered, releaseStop := make(chan struct{}), make(chan struct{})
				h.stop = func() error { close(entered); <-h.reaped; <-releaseStop; return tc.stopErr }
				snap := launchStopFixture(t, p, h, 0)
				first := stopAsync(p, snap.ID, time.Second)
				<-entered
				concurrent := stopAsync(p, snap.ID, time.Second)
				synctest.Wait()
				h.finish(0)
				<-h.reaped
				synctest.Wait()
				early := false
				for _, result := range []<-chan error{first, concurrent} {
					select {
					case err := <-result:
						t.Errorf("stop returned before attempt result: %v", err)
						early = true
					default:
					}
				}
				close(releaseStop)
				if !early {
					for _, result := range []<-chan error{first, concurrent} {
						if err := <-result; !errors.Is(err, tc.stopErr) {
							t.Errorf("stop error = %v, want %v", err, tc.stopErr)
						}
					}
				}
				waitUntilFinished(t, p, "s1", snap.ID, tc.want)
			})
		})
	}
}

func TestTimeoutStopCanWaitForHandleExit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := NewWithRunner(Config{}, &stubRunner{})
		h := &orderedStopHandle{stubHandle: &stubHandle{release: make(chan struct{})}, reaped: make(chan struct{})}
		h.stop = func() error { h.finish(143); <-h.reaped; return nil }
		snap := launchStopFixture(t, p, h, 1)
		waitUntilFinished(t, p, "s1", snap.ID, StatusTimedOut)
	})
}
